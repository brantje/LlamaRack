package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

type runtimeWebSocketHandler struct {
	auth           *auth.Service
	lifecycle      *lifecycle.Service
	allowedOrigins string
}

type runtimeEvent struct {
	Type    string             `json:"type"`
	Runtime supervisor.Runtime `json:"runtime"`
}

type runtimeSnapshotEvent struct {
	Type     string               `json:"type"`
	Runtimes []supervisor.Runtime `json:"runtimes"`
}

func NewRuntimeWebSocketHandler(a *auth.Service, l *lifecycle.Service, allowedOrigins string) http.Handler {
	return &runtimeWebSocketHandler{auth: a, lifecycle: l, allowedOrigins: allowedOrigins}
}

func (h *runtimeWebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if _, err := h.auth.SessionUser(r.Context(), cookie.Value); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(request *http.Request) bool {
		return websocketOriginAllowed(request, h.allowedOrigins)
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	snapshot, events, cancel, err := h.lifecycle.SubscribeRuntimes(r.Context())
	if err != nil {
		return
	}
	defer cancel()
	if err := conn.WriteJSON(runtimeSnapshotEvent{Type: "runtime_snapshot", Runtimes: snapshot}); err != nil {
		return
	}

	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-disconnected:
			return
		case runtime, open := <-events:
			if !open {
				return
			}
			if err := conn.WriteJSON(runtimeEvent{Type: "runtime", Runtime: runtime}); err != nil {
				return
			}
		}
	}
}

func websocketOriginAllowed(r *http.Request, configured string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range strings.Split(configured, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Hostname() == "" || (originURL.Scheme != "http" && originURL.Scheme != "https") {
		return false
	}
	requestURL, err := url.Parse("http://" + r.Host)
	if err != nil || requestURL.Hostname() == "" {
		return false
	}
	return strings.EqualFold(originURL.Hostname(), requestURL.Hostname())
}
