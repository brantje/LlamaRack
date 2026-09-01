package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

type systemLogStore interface {
	Snapshot(int) []systemlog.Entry
	Subscribe(int) ([]systemlog.Entry, <-chan systemlog.Entry, func())
}

type systemLogHandler struct{ store systemLogStore }

func NewSystemLogHandler(store systemLogStore) http.Handler {
	if store == nil {
		store = systemlog.Default
	}
	return &systemLogHandler{store: store}
}

func (h *systemLogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, ok := systemLogLimit(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 4000"})
		return
	}
	level, ok := normalizeSystemLogLevel(r.URL.Query().Get("level"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be all, INFO, WARN, DEBUG or ERROR"})
		return
	}
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/stream") {
		h.stream(w, r, limit, level, source, query)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": limitedSystemLogEntries(h.store.Snapshot(systemLogScanLimit), limit, level, source, query)})
}

func systemLogLimit(r *http.Request) (int, bool) {
	limit := 100
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return limit, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 4000 {
		return 0, false
	}
	return value, true
}

func normalizeSystemLogLevel(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "ALL", true
	}
	switch value {
	case "ALL", "INFO", "WARN", "DEBUG", "ERROR":
		return value, true
	default:
		return "", false
	}
}

func systemLogMatches(entry systemlog.Entry, level, source, query string) bool {
	if level == "WARN" {
		if entry.Level != systemlog.Warn && entry.Level != systemlog.Error {
			return false
		}
	} else if level != "ALL" && string(entry.Level) != level {
		return false
	}
	if source != "" && source != "all" && entry.Source != source {
		return false
	}
	return query == "" || strings.Contains(strings.ToLower(entry.Message), strings.ToLower(query))
}

func filterSystemLogEntries(entries []systemlog.Entry, level, source, query string) []systemlog.Entry {
	out := make([]systemlog.Entry, 0, len(entries))
	for _, entry := range entries {
		if systemLogMatches(entry, level, source, query) {
			out = append(out, entry)
		}
	}
	return out
}

const systemLogScanLimit = 4000

func limitedSystemLogEntries(entries []systemlog.Entry, limit int, level, source, query string) []systemlog.Entry {
	return systemlog.LimitPerSource(filterSystemLogEntries(entries, level, source, query), limit)
}

func (h *systemLogHandler) stream(w http.ResponseWriter, r *http.Request, limit int, level, source, query string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	snapshot, events, cancel := h.store.Subscribe(systemLogScanLimit)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writeEvent := func(name string, value any) bool {
		payload, err := json.Marshal(value)
		if err != nil {
			return false
		}
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
		return err == nil
	}
	if !writeEvent("snapshot", limitedSystemLogEntries(snapshot, limit, level, source, query)) {
		return
	}
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case entry, open := <-events:
			if !open {
				return
			}
			if !systemLogMatches(entry, level, source, query) {
				continue
			}
			if !writeEvent("log", entry) {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
