package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
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
		if path == "/api/v1/health" || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if path == "/api/v1/auth/bootstrap" || path == "/api/v1/auth/login" {
			if isStateChanging(r.Method) && !network.OriginAllowed(r, r.Header.Get("Origin")) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "request origin is not allowed"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		sessionCookieValue, err := r.Cookie(sessionCookie)
		if err != nil || sessionCookieValue.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		user, session, err := a.SessionUserWithSession(r.Context(), sessionCookieValue.Value)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if isStateChanging(r.Method) {
			csrfHeader := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
			csrfCookieValue, cookieErr := r.Cookie(csrfCookie)
			if cookieErr != nil || csrfHeader == "" || subtle.ConstantTimeCompare([]byte(csrfCookieValue.Value), []byte(csrfHeader)) != 1 || a.ValidateCSRF(r.Context(), sessionCookieValue.Value, csrfHeader) != nil {
				slog.Warn("security event", "event", "csrf.rejected", "user_id", user.ID, "path", path)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf validation failed"})
				return
			}
		}
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, managementAuthContext{User: user, Session: session})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func managementAuthFromRequest(r *http.Request) (auth.User, auth.Session, bool) {
	value, ok := r.Context().Value(managementAuthContextKey{}).(managementAuthContext)
	return value.User, value.Session, ok
}

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

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
