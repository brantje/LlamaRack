package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestRuntimeWebSocketRequiresSessionAndStreamsSupervisorState(t *testing.T) {
	f := newAPIFixture(t, nil)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/ws", NewRuntimeWebSocketHandler(f.auth, f.server.lifecycle))
	mux.Handle("/", f.server)
	server := httptest.NewServer(mux)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"

	if conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		_ = conn.Close()
		t.Fatal("expected unauthenticated websocket handshake to fail")
	} else if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated response=%v err=%v", response, err)
	}

	cookie := bootstrapAndLogin(t, f)
	badOriginHeaders := http.Header{}
	badOriginHeaders.Set("Cookie", cookie.String())
	badOriginHeaders.Set("Origin", "https://evil.example")
	if conn, response, err := websocket.DefaultDialer.Dial(wsURL, badOriginHeaders); err == nil {
		_ = conn.Close()
		t.Fatal("expected cross-host websocket origin to fail")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-host response=%v err=%v", response, err)
	}

	headers := http.Header{}
	headers.Set("Cookie", cookie.String())
	headers.Set("Origin", server.URL)
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("websocket dial failed: response=%v err=%v", response, err)
	}
	defer conn.Close()

	model := createModel(t, f, cookie)
	start := doRequest(t, f.server, http.MethodPost, "/api/v1/models/"+model.ID+"/start", nil, cookie)
	if start.Code != http.StatusServiceUnavailable {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []supervisor.State{supervisor.Starting, supervisor.Failed} {
		var event runtimeEvent
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatalf("read %s event: %v", want, err)
		}
		if event.Type != "runtime" || event.Runtime.ModelID != model.ID || event.Runtime.State != want {
			t.Fatalf("event=%+v want state=%s model=%s", event, want, model.ID)
		}
	}
}

func TestRuntimeWebSocketMethodAndOriginValidation(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := NewRuntimeWebSocketHandler(f.auth, f.server.lifecycle)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ws", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST websocket status=%d", response.Code)
	}

	for _, tc := range []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "no origin", host: "manager.test:8888", want: true},
		{name: "same host different port", host: "manager.test:8888", origin: "http://manager.test:3000", want: true},
		{name: "different host", host: "manager.test:8888", origin: "https://evil.example", want: false},
		{name: "invalid scheme", host: "manager.test:8888", origin: "file:///tmp/index.html", want: false},
		{name: "invalid request host", host: "://", origin: "http://manager.test", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := websocketOriginAllowed(r); got != tc.want {
				t.Fatalf("websocketOriginAllowed=%v want=%v", got, tc.want)
			}
		})
	}
}
