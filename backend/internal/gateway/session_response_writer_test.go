package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error { b.closed = true; return nil }

// A diagnostic wrapper is transparent to Go's response-controller unwrapping
// and preserves the first response status when downstream flushes or writes.
func TestDiagnosticResponseWriterStatusAndUnwrap(t *testing.T) {
	t.Run("implicit write", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		w := &diagnosticResponseWriter{ResponseWriter: recorder}
		if w.Unwrap() != recorder || w.StatusCode() != http.StatusOK {
			t.Fatal("unwrapped writer or initial status is incorrect")
		}
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		if w.StatusCode() != http.StatusOK || recorder.Body.String() != "hello" {
			t.Fatalf("implicit status=%d body=%q", w.StatusCode(), recorder.Body.String())
		}
	})
	t.Run("explicit error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		w := &diagnosticResponseWriter{ResponseWriter: recorder}
		w.WriteHeader(http.StatusTooManyRequests)
		w.WriteHeader(http.StatusOK)
		if w.StatusCode() != http.StatusTooManyRequests || recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("lost original error status: %d/%d", w.StatusCode(), recorder.Code)
		}
	})
	t.Run("flush", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		w := &diagnosticResponseWriter{ResponseWriter: recorder}
		w.Flush()
		if w.StatusCode() != http.StatusOK || !recorder.Flushed {
			t.Fatalf("flush not forwarded: status=%d flushed=%v", w.StatusCode(), recorder.Flushed)
		}
	})
}

func TestSessionPeekRestoresBodyAndClosesOriginal(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantSession string
	}{
		{"valid", `{"model":"coder","session_id":"session-1"}`, "session-1"},
		{"malformed", `{"model":`, ""},
		{"not-json", "hello", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &closeTrackingBody{Reader: strings.NewReader(tc.body)}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Body = original
			capture := &sessionCapture{}
			gotReq := peekSessionCapture(req, capture)
			payload, err := io.ReadAll(gotReq.Body)
			if err != nil || !bytes.Equal(payload, []byte(tc.body)) || capture.sessionID != tc.wantSession {
				t.Fatalf("restored payload=%q session=%q err=%v", payload, capture.sessionID, err)
			}
			if err := gotReq.Body.Close(); err != nil || !original.closed {
				t.Fatalf("underlying body not closed: %v", err)
			}
		})
	}
	var nilBody *prefixRestoreBody
	_ = nilBody
	if err := (prefixRestoreBody{Reader: strings.NewReader("")}).Close(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("closing an empty source should succeed: %v", err)
	}
}
