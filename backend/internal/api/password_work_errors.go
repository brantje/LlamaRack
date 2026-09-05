package api

import (
	"net/http"
)

func writeLoginPasswordWorkBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "invalid username or password"})
}

func writePasswordWorkUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "password processing is temporarily unavailable"})
}
