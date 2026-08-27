package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
)

type phase10AuthHandler struct {
	auth      *auth.Service
	network   *managersecurity.Network
	protector *managersecurity.LoginProtector
}

func NewPhase10AuthHandler(a *auth.Service, network *managersecurity.Network, protector *managersecurity.LoginProtector) http.Handler {
	return &phase10AuthHandler{auth: a, network: network, protector: protector}
}

func (h *phase10AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/auth/bootstrap" && r.Method == http.MethodGet:
		required, err := h.auth.BootstrapRequired(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"required": required})
	case path == "/api/v1/auth/bootstrap" && r.Method == http.MethodPost:
		h.bootstrap(w, r)
	case path == "/api/v1/auth/login" && r.Method == http.MethodPost:
		h.login(w, r)
	case path == "/api/v1/auth/logout" && r.Method == http.MethodPost:
		h.logout(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *phase10AuthHandler) bootstrap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	user, err := h.auth.Bootstrap(r.Context(), in.Username, in.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	slog.Info("security event", "event", "user.created", "target_user_id", user.ID, "bootstrap", true)
	writeJSON(w, http.StatusCreated, user)
}

func (h *phase10AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	address := h.network.EffectiveRemoteAddress(r)
	delay, locked := h.protector.BeforeAttempt(r.Context(), in.Username, address)
	if locked {
		retry := int(delay.Seconds())
		if retry < 1 { retry = 1 }
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		slog.Warn("security event", "event", "auth.login_rate_limited", "remote_address", address)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "invalid username or password"})
		return
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	token, csrf, user, err := h.auth.LoginWithMetadata(r.Context(), in.Username, in.Password, address, r.UserAgent())
	if err != nil {
		locked = h.protector.Failure(r.Context(), in.Username, address)
		event := "auth.login_failure"
		if locked { event = "auth.login_rate_limited" }
		slog.Warn("security event", "event", event, "remote_address", address)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	h.protector.Success(in.Username, address)
	SetSessionCookies(w, token, csrf, h.auth.SessionLifetime(), h.network.IsSecure(r))
	slog.Info("security event", "event", "auth.login_success", "user_id", user.ID, "remote_address", address)
	writeJSON(w, http.StatusOK, user)
}

func (h *phase10AuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	user, _, _ := managementAuthFromRequest(r)
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = h.auth.Logout(r.Context(), cookie.Value)
	}
	ClearSessionCookies(w, h.network.IsSecure(r))
	if user.ID != 0 {
		slog.Info("security event", "event", "session.revoked", "user_id", user.ID, "current", true)
	}
	w.WriteHeader(http.StatusNoContent)
}
