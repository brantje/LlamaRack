package systemlog

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type Level string

const (
	Info  Level = "INFO"
	Warn  Level = "WARN"
	Debug Level = "DEBUG"
	Error Level = "ERROR"
)

type Entry struct {
	Timestamp string `json:"timestamp"`
	Level     Level  `json:"level"`
	Source    string `json:"source"`
	Message   string `json:"message"`
}

type Store struct {
	mu     sync.Mutex
	max    int
	data   []Entry
	offset int
	full   bool
	subs   map[chan Entry]struct{}
	now    func() time.Time
}

func New(max int) *Store {
	if max < 1 {
		max = 4000
	}
	return &Store{max: max, data: make([]Entry, max), subs: map[chan Entry]struct{}{}, now: time.Now}
}

// Default is the manager-wide in-memory diagnostic stream. It deliberately
// shares process lifetime with the manager and contains no persisted secrets.
var Default = New(4000)

func (s *Store) Add(level Level, source, message string) {
	if s == nil {
		return
	}
	source = strings.TrimSpace(source)
	message = strings.TrimSpace(message)
	if source == "" || message == "" || !validLevel(level) {
		return
	}
	entry := Entry{
		Timestamp: s.now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Level:     level,
		Source:    source,
		Message:   message,
	}
	s.mu.Lock()
	s.data[s.offset] = entry
	s.offset++
	if s.offset == s.max {
		s.offset = 0
		s.full = true
	}
	for ch := range s.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	s.mu.Unlock()
}

func (s *Store) Snapshot(limit int) []Entry {
	if s == nil || limit <= 0 {
		return []Entry{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return lastPerSource(s.entriesLocked(), limit)
}

func (s *Store) Subscribe(limit int) ([]Entry, <-chan Entry, func()) {
	if s == nil {
		ch := make(chan Entry)
		close(ch)
		return []Entry{}, ch, func() {}
	}
	s.mu.Lock()
	entries := s.entriesLocked()
	var snapshot []Entry
	if limit > 0 {
		snapshot = lastPerSource(entries, limit)
	} else {
		snapshot = entries
	}
	ch := make(chan Entry, 256)
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			close(ch)
			s.mu.Unlock()
		})
	}
	return snapshot, ch, cancel
}

func (s *Store) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.offset = 0
	s.full = false
	s.mu.Unlock()
}

func (s *Store) entriesLocked() []Entry {
	if !s.full {
		out := make([]Entry, s.offset)
		copy(out, s.data[:s.offset])
		return out
	}
	out := make([]Entry, s.max)
	n := copy(out, s.data[s.offset:])
	copy(out[n:], s.data[:s.offset])
	return out
}

// LimitPerSource keeps the newest `limit` entries for each source, preserving order.
func LimitPerSource(entries []Entry, limit int) []Entry {
	return lastPerSource(entries, limit)
}

func lastPerSource(entries []Entry, limit int) []Entry {
	if limit <= 0 || len(entries) == 0 {
		return []Entry{}
	}
	counts := make(map[string]int)
	keep := make([]bool, len(entries))
	kept := 0
	for i := len(entries) - 1; i >= 0; i-- {
		source := entries[i].Source
		if counts[source] < limit {
			keep[i] = true
			counts[source]++
			kept++
		}
	}
	out := make([]Entry, 0, kept)
	for i, entry := range entries {
		if keep[i] {
			out = append(out, entry)
		}
	}
	return out
}

func validLevel(level Level) bool {
	switch level {
	case Info, Warn, Debug, Error:
		return true
	default:
		return false
	}
}

func Log(level Level, source, message string) { Default.Add(level, source, message) }

func FormatDuration(duration time.Duration) string {
	if duration < time.Second {
		return strconv.FormatInt(duration.Milliseconds(), 10) + "ms"
	}
	return duration.Round(10 * time.Millisecond).String()
}
