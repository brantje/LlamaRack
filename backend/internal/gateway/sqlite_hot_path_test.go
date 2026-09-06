package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWarmV1HotPathDoesNotUseSQLite(t *testing.T) {
	fixture := newGatewayFixture(t, true)

	writebackCtx, cancelWriteback := context.WithCancel(context.Background())
	fixture.observability.StartWriteback(writebackCtx)
	cancelWriteback()

	handler := WithRequestLogContext(fixture.gateway, fixture.observability)
	prewarm := gatewayRequest(t, handler, http.MethodPost, "/v1/chat/completions", fixture.secret, `{"model":"gateway-model","messages":[{"role":"user","content":"warm"}]}`)
	if prewarm.Code != http.StatusOK {
		t.Fatalf("prewarm status=%d body=%s", prewarm.Code, prewarm.Body.String())
	}

	conn, err := fixture.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		status int
		body   string
	}
	completed := make(chan result, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gateway-model","messages":[{"role":"user","content":"hot"}]}`))
		request.Header.Set("Authorization", "Bearer "+fixture.secret)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		completed <- result{status: response.Code, body: response.Body.String()}
	}()

	select {
	case got := <-completed:
		if got.status != http.StatusOK {
			_ = conn.Close()
			t.Fatalf("hot request status=%d body=%s", got.status, got.body)
		}
	case <-time.After(750 * time.Millisecond):
		_ = conn.Close()
		t.Fatal("warm /v1 request blocked while SQLite's sole connection was occupied")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFlush()
	if err := fixture.observability.Flush(flushCtx); err != nil {
		t.Fatalf("flush buffered observability: %v", err)
	}
}
