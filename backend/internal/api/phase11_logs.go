package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type phase11LogSource interface {
	Logs(string) []string
	SubscribeLogs(string) ([]string, <-chan string, func())
}

type logEntry struct {
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

type phase11LogHandler struct{ source phase11LogSource }

func NewPhase11LogHandler(source phase11LogSource) http.Handler { return &phase11LogHandler{source: source} }

func (h *phase11LogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "instance_id is required"})
		return
	}
	source, ok := normalizeLogSource(r.URL.Query().Get("source"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source must be all, stdout, stderr or manager"})
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 2000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 2000"})
			return
		}
		limit = value
	}
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/stream") {
		h.stream(w, r, instanceID, source, query, limit)
		return
	}
	entries := filterLogEntries(h.source.Logs(instanceID), source, query, limit)
	writeJSON(w, http.StatusOK, map[string]any{"instance_id": instanceID, "entries": entries})
}

func (h *phase11LogHandler) stream(w http.ResponseWriter, r *http.Request, instanceID, source, query string, limit int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	snapshot, events, cancel := h.source.SubscribeLogs(instanceID)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writeEntry := func(entry logEntry) bool {
		payload, err := json.Marshal(entry)
		if err != nil { return false }
		if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload); err != nil { return false }
		return true
	}
	for _, entry := range filterLogEntries(snapshot, source, query, limit) {
		if !writeEntry(entry) { return }
	}
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done(): return
		case line, open := <-events:
			if !open { return }
			entry, ok := parseLogEntry(line)
			if !ok || !logEntryMatches(entry, source, query) { continue }
			if !writeEntry(entry) { return }
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil { return }
			flusher.Flush()
		}
	}
}

func normalizeLogSource(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" { value = "all" }
	switch value {
	case "all", "stdout", "stderr", "manager": return value, true
	default: return "", false
	}
}

func parseLogEntry(line string) (logEntry, bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 { return logEntry{}, false }
	source := strings.TrimSuffix(strings.TrimPrefix(parts[0], "["), "]")
	if source != "stdout" && source != "stderr" && source != "manager" { return logEntry{}, false }
	if _, err := time.Parse(time.RFC3339Nano, parts[1]); err != nil { return logEntry{}, false }
	return logEntry{Source: source, Timestamp: parts[1], Text: parts[2]}, true
}

func logEntryMatches(entry logEntry, source, query string) bool {
	if source != "all" && entry.Source != source { return false }
	if query == "" { return true }
	return strings.Contains(strings.ToLower(entry.Text), strings.ToLower(query))
}

func filterLogEntries(lines []string, source, query string, limit int) []logEntry {
	if limit <= 0 { return []logEntry{} }
	entries := make([]logEntry, 0, min(len(lines), limit))
	for _, line := range lines {
		entry, ok := parseLogEntry(line)
		if ok && logEntryMatches(entry, source, query) { entries = append(entries, entry) }
	}
	if len(entries) > limit { entries = entries[len(entries)-limit:] }
	return entries
}
