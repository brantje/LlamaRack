package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginPasswordWorkBusyResponseStaysGeneric(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeLoginPasswordWorkBusy(recorder)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d", recorder.Code)
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q", got)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body["error"]; got != "invalid username or password" {
		t.Fatalf("error=%q", got)
	}
}

func TestPasswordMutationWorkBusyResponseIsTemporaryUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	writePasswordWorkUnavailable(recorder)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", recorder.Code)
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q", got)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body["error"]; got != "password processing is temporarily unavailable" {
		t.Fatalf("error=%q", got)
	}
}
