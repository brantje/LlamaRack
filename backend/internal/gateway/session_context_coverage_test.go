package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/observability"
)

type closeTrackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *closeTrackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestSessionCaptureBodyCloseClosesSource(t *testing.T) {
	reader, writer := io.Pipe()
	source := &closeTrackingReadCloser{Reader: strings.NewReader("body")}
	body := &sessionCaptureBody{source: source, pipe: writer}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if !source.closed {
		t.Fatal("source body was not closed")
	}
}

func TestWithRequestLogContextWithoutRequestIDSkipsContextWrite(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := observability.New(db)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	WithRequestLogContext(next, service).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d", called, w.Code)
	}
}
