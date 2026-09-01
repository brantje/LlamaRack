package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

const (
	sessionCookie = "lcm_session"
	csrfCookie    = "lcm_csrf"
)

type managementAuthContext struct {
	User    auth.User
	Session auth.Session
}

type managementAuthContextKey struct{}

type managementStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *managementStatusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *managementStatusRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}
func (w *managementStatusRecorder) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func ManagementSecurity(a *auth.Service, network *managersecurity.Network, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "" {
			path = "/"
		}
		// The Playground bridge re-enters the inference gateway, which enforces
		// its own 32 MiB request limit. Keep the generic 1 MiB management mutation
		// cap for every other state-changing endpoint.
		if isStateChanging(r.Method) && path != "/api/v1/playground/chat/completions" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		if publicManagementRequest(path, r.Method) {
			if (path == "/api/v1/auth/bootstrap" || path == "/api/v1/auth/login") && isStateChanging(r.Method) && !network.OriginAllowed(r, r.Header.Get("Origin")) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "request origin is not allowed"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		var (
			user    auth.User
			session auth.Session
			err     error
		)
		token := bearerToken(r.Header.Get("Authorization"))
		switch {
		case token != "":
			user, session, err = a.AuthenticateBearer(r.Context(), token)
		case browserStreamTicketRequest(path, r.Method):
			// Native EventSource and WebSocket cannot attach an Authorization
			// header. Reuse the same short-lived, one-time ticket mechanism as
			// the runtime WebSocket so bearer credentials never have to be
			// placed in the stream URL.
			user, session, err = a.ConsumeWebSocketTicket(r.Context(), r.URL.Query().Get("ticket"))
		default:
			err = auth.ErrSessionInvalid
		}
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, managementAuthContext{User: user, Session: session})
		request := r.WithContext(ctx)
		if instanceID, action, ok := lifecycleAction(path, r.Method); ok {
			recorder := &managementStatusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, request)
			if recorder.StatusCode() < http.StatusBadRequest {
				systemlog.Log(systemlog.Info, "manager", fmt.Sprintf("user %s %s %s", user.Username, action, instanceID))
			}
			return
		}
		next.ServeHTTP(w, request)
	})
}

func lifecycleAction(path, method string) (instanceID, action string, ok bool) {
	if method != http.MethodPost || !strings.HasPrefix(path, "/api/v1/instances/") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/instances/"), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", "", false
	}
	switch parts[1] {
	case "start":
		action = "started"
	case "stop":
		action = "stopped"
	case "restart":
		action = "restarted"
	case "kill":
		action = "killed"
	default:
		return "", "", false
	}
	return parts[0], action, true
}

func browserStreamTicketRequest(path, method string) bool {
	if method != http.MethodGet {
		return false
	}
	return path == "/api/v1/logs/stream" || path == "/api/v1/downloads/ws"
}

func publicManagementRequest(path, method string) bool {
	if path == "/api/v1/health" || path == "/health" || path == "/api/v1/ws" {
		return true
	}
	if path == "/api/v1/auth/bootstrap" || path == "/api/v1/auth/login" || path == "/api/v1/auth/providers" {
		return true
	}
	if path == "/api/v1/auth/oidc/exchange" {
		return method == http.MethodPost
	}
	if strings.HasPrefix(path, "/api/v1/auth/oidc/") {
		return method == http.MethodGet && (strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/callback"))
	}
	return false
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func managementAuthFromRequest(r *http.Request) (auth.User, auth.Session, bool) {
	value, ok := r.Context().Value(managementAuthContextKey{}).(managementAuthContext)
	return value.User, value.Session, ok
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// SetSessionCookies and ClearSessionCookies are retained only for legacy unit-test
// fixtures while the management transport migrates to bearer JWTs. Production
// authentication does not read these cookies.
func SetSessionCookies(w http.ResponseWriter, token, csrf string, lifetime time.Duration, secure bool) {
	maxAge := int(lifetime.Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func ClearSessionCookies(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: "", Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
