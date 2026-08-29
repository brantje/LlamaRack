package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
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

func ManagementSecurity(a *auth.Service, network *managersecurity.Network, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "" {
			path = "/"
		}
		if isStateChanging(r.Method) {
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
		case path == "/api/v1/logs/stream" && r.Method == http.MethodGet:
			// Native EventSource cannot attach an Authorization header. Reuse the
			// same short-lived, one-time ticket mechanism as the runtime WebSocket
			// so bearer credentials never have to be placed in the stream URL.
			user, session, err = a.ConsumeWebSocketTicket(r.Context(), r.URL.Query().Get("ticket"))
		default:
			err = auth.ErrSessionInvalid
		}
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, managementAuthContext{User: user, Session: session})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
