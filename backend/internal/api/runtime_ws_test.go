package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestRuntimeWebSocketRequiresTicketAndStreamsSupervisorState(t *testing.T) {
	f := newAPIFixture(t, nil)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/ws", NewRuntimeWebSocketHandler(f.auth, f.server.lifecycle, ""))
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
	login, err := f.auth.LoginBearerWithMetadata(t.Context(), "admin", "correct-horse-battery", "127.0.0.1", "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := f.auth.AuthenticateBearer(t.Context(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	issueTicket := func() string {
		t.Helper()
		ticket, _, err := f.auth.IssueWebSocketTicket(t.Context(), session)
		if err != nil {
			t.Fatal(err)
		}
		return ticket
	}

	badOriginHeaders := http.Header{}
	badOriginHeaders.Set("Origin", "https://evil.example")
	if conn, response, err := websocket.DefaultDialer.Dial(wsURL+"?ticket="+issueTicket(), badOriginHeaders); err == nil {
		_ = conn.Close()
		t.Fatal("expected cross-host websocket origin to fail")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-host response=%v err=%v", response, err)
	}

	model := createModel(t, f, cookie)
	instance, err := f.server.lifecycle.Instances().Create(context.Background(), instances.CreateInput{ModelID: model.ID, Name: "WS instance"})
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set("Origin", server.URL)
	ticket := issueTicket()
	conn, response, err := websocket.DefaultDialer.Dial(wsURL+"?ticket="+ticket, headers)
	if err != nil {
		t.Fatalf("websocket dial failed: response=%v err=%v", response, err)
	}
	defer conn.Close()
	if replay, replayResponse, replayErr := websocket.DefaultDialer.Dial(wsURL+"?ticket="+ticket, headers); replayErr == nil {
		_ = replay.Close()
		t.Fatal("expected websocket ticket replay to fail")
	} else if replayResponse == nil || replayResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket replay response=%v err=%v", replayResponse, replayErr)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	var snapshot runtimeSnapshotEvent
	if err := conn.ReadJSON(&snapshot); err != nil {
		t.Fatalf("read runtime snapshot: %v", err)
	}
	if snapshot.Type != "runtime_snapshot" || len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].InstanceID != instance.ID || snapshot.Runtimes[0].ModelID != model.ID || snapshot.Runtimes[0].State != supervisor.Unloaded {
		t.Fatalf("snapshot=%+v", snapshot)
	}

	start := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/"+instance.Slug+"/start", nil, cookie)
	if start.Code != http.StatusServiceUnavailable {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	for _, want := range []supervisor.State{supervisor.Starting, supervisor.Failed} {
		var event runtimeEvent
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatalf("read %s event: %v", want, err)
		}
		if event.Type != "runtime" || event.Runtime.InstanceID != instance.ID || event.Runtime.ModelID != model.ID || event.Runtime.State != want {
			t.Fatalf("event=%+v want state=%s instance=%s", event, want, instance.ID)
		}
	}
}

func TestRuntimeWebSocketMethodAndOriginValidation(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := NewRuntimeWebSocketHandler(f.auth, f.server.lifecycle, "")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ws", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST websocket status=%d", response.Code)
	}

	for _, tc := range []struct {
		name       string
		host       string
		origin     string
		configured string
		want       bool
	}{
		{name: "no origin", host: "manager.test:8888", want: true},
		{name: "same host different port", host: "manager.test:8888", origin: "http://manager.test:3000", want: true},
		{name: "configured cross host", host: "manager.test:8888", origin: "https://ui.example", configured: "https://ui.example", want: true},
		{name: "configured list", host: "manager.test:8888", origin: "https://ui.example", configured: "https://other.example, https://ui.example ", want: true},
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
			if got := websocketOriginAllowed(r, tc.configured); got != tc.want {
				t.Fatalf("websocketOriginAllowed=%v want=%v", got, tc.want)
			}
		})
	}
}
