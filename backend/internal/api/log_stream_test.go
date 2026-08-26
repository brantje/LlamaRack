package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerLogStreamRequiresAuthAndStreamsSSE(t *testing.T) {
	f := newAPIFixture(t, nil)

	unauthorized := doRequest(t, f.server, http.MethodGet, "/api/v1/instances/instance-1/logs/stream", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	cookie := bootstrapAndLogin(t, f, "admin")
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/instance-1/logs/stream", nil).WithContext(ctx)
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.server.ServeHTTP(recorder, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("log stream did not close after request cancellation")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type=%q", got)
	}
	if !strings.Contains(recorder.Body.String(), ": connected") {
		t.Fatalf("missing connected event: %q", recorder.Body.String())
	}
}
