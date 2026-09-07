package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/observability"
	"github.com/brantje/llamarack/backend/internal/systemlog"
)

const (
	sessionCookie = "llamarack_session"
	csrfCookie    = "llamarack_csrf"
)

type managementAuthContext struct {
	User    *auth.User
	Session *auth.Session
	APIKey  *auth.APIKey
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

		principal, err := authenticateManagementPrincipal(a, r, path)
		if err != nil {
			status := http.StatusUnauthorized
			message := "authentication required"
			if forbidden, ok := err.(*managementForbiddenError); ok {
				status = http.StatusForbidden
				message = forbidden.message
			}
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, principal)
		if playgroundPath(path) && principal.User != nil {
			ctx = auth.WithTrustedInferenceContext(ctx, auth.TrustedInferencePrincipal{
				Kind: observability.OwnerKindManagementUser,
				ID:   strconv.FormatInt(principal.User.ID, 10),
			})
		}
		request := r.WithContext(ctx)
		if instanceID, action, ok := lifecycleAction(path, r.Method); ok {
			recorder := &managementStatusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, request)
			if recorder.StatusCode() < http.StatusBadRequest {
				systemlog.Log(systemlog.Info, "manager", fmt.Sprintf("%s %s %s", principal.ActorLabel(), action, instanceID))
			}
			return
		}
		next.ServeHTTP(w, request)
	})
}

type managementForbiddenError struct{ message string }

func (e *managementForbiddenError) Error() string { return e.message }

func authenticateManagementPrincipal(a *auth.Service, r *http.Request, path string) (managementAuthContext, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	switch {
	case strings.HasPrefix(token, "sk-"):
		key, err := a.AuthenticateAPIKeyInfo(r.Context(), token)
		if err != nil {
			return managementAuthContext{}, err
		}
		if key.KeyType == auth.APIKeyTypeInference {
			return managementAuthContext{}, &managementForbiddenError{message: "this API key cannot access the management API"}
		}
		if apiKeySessionDenied(path) || apiKeyUserPrincipalRequired(path, r.Method) {
			return managementAuthContext{}, &managementForbiddenError{message: "this API key cannot access this endpoint"}
		}
		return managementAuthContext{APIKey: &key}, nil
	case token != "":
		user, session, err := a.AuthenticateBearer(r.Context(), token)
		if err != nil {
			return managementAuthContext{}, err
		}
		return managementAuthContext{User: &user, Session: &session}, nil
	case browserStreamTicketRequest(path, r.Method):
		// Native EventSource and WebSocket cannot attach an Authorization
		// header. Reuse the same short-lived, one-time ticket mechanism as
		// the runtime WebSocket so bearer credentials never have to be
		// placed in the stream URL.
		user, session, err := a.ConsumeWebSocketTicket(r.Context(), r.URL.Query().Get("ticket"))
		if err != nil {
			return managementAuthContext{}, err
		}
		return managementAuthContext{User: &user, Session: &session}, nil
	default:
		return managementAuthContext{}, auth.ErrSessionInvalid
	}
}

func (c managementAuthContext) ActorLabel() string {
	if c.User != nil {
		return "user " + c.User.Username
	}
	if c.APIKey != nil {
		return "api_key:" + c.APIKey.ID
	}
	return "unknown"
}

func (c managementAuthContext) CreatedByUserID() int64 {
	if c.User != nil {
		return c.User.ID
	}
	return 0
}

func (c managementAuthContext) UserSession() (auth.User, auth.Session, bool) {
	if c.User == nil || c.Session == nil {
		return auth.User{}, auth.Session{}, false
	}
	return *c.User, *c.Session, true
}

func actorLogAttrs(principal managementAuthContext) []any {
	if principal.User != nil {
		return []any{"actor_user_id", principal.User.ID}
	}
	if principal.APIKey != nil {
		return []any{"actor_api_key_id", principal.APIKey.ID}
	}
	return nil
}

func requireManagementUserPrincipal(w http.ResponseWriter, principal managementAuthContext) bool {
	if principal.User != nil {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
	return false
}

func apiKeySessionDenied(path string) bool {
	if path == "/api/v1/me" || strings.HasPrefix(path, "/api/v1/me/") {
		return true
	}
	if path == "/api/v1/auth/logout" || path == "/api/v1/auth/ws-ticket" {
		return true
	}
	if path == "/api/v1/logs/stream" || path == "/api/v1/downloads/ws" {
		return true
	}
	return playgroundPath(path)
}

func playgroundPath(path string) bool {
	return path == "/api/v1/playground" || strings.HasPrefix(path, "/api/v1/playground/")
}

func apiKeyUserPrincipalRequired(path, method string) bool {
	if path == "/api/v1/users" || strings.HasPrefix(path, "/api/v1/users/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/sessions/") {
		return true
	}
	if path == "/api/v1/admin/service-accounts" || strings.HasPrefix(path, "/api/v1/admin/service-accounts/") {
		return true
	}
	if path == "/api/v1/admin/auth" || strings.HasPrefix(path, "/api/v1/admin/auth/") {
		return true
	}
	if path == "/api/v1/api-keys" || strings.HasPrefix(path, "/api/v1/api-keys/") {
		return isStateChanging(method)
	}
	if path == "/api/v1/litellm" || strings.HasPrefix(path, "/api/v1/litellm/") {
		if path == "/api/v1/litellm/test" && method == http.MethodPost {
			return false
		}
		return isStateChanging(method)
	}
	return false
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

func managementAuthFromRequest(r *http.Request) (managementAuthContext, bool) {
	value, ok := r.Context().Value(managementAuthContextKey{}).(managementAuthContext)
	return value, ok
}

func managementUserFromRequest(r *http.Request) (auth.User, auth.Session, bool) {
	principal, ok := managementAuthFromRequest(r)
	if !ok || principal.User == nil || principal.Session == nil {
		return auth.User{}, auth.Session{}, false
	}
	return *principal.User, *principal.Session, true
}

func sessionCookieValue(r *http.Request) string {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		return cookie.Value
	}
	return ""
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
