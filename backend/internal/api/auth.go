package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

type loginHandler struct {
	auth      *auth.Service
	network   *managersecurity.Network
	protector *managersecurity.LoginProtector
	settings  *settings.Service
}

func NewAuthHandler(a *auth.Service, network *managersecurity.Network, protector *managersecurity.LoginProtector, managerSettings *settings.Service) http.Handler {
	return &loginHandler{auth: a, network: network, protector: protector, settings: managerSettings}
}
func (h *loginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
func (h *loginHandler) bootstrap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	user, err := h.auth.Bootstrap(r.Context(), in.Username, in.Password)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if errors.Is(err, auth.ErrPasswordWorkBusy) {
			writePasswordWorkUnavailable(w)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	slog.Info("security event", "event", "user.created", "target_user_id", user.ID, "bootstrap", true)
	writeJSON(w, http.StatusCreated, user)
}
func (h *loginHandler) login(w http.ResponseWriter, r *http.Request) {
	enabled, err := h.settings.Bool(r.Context(), settings.LocalLoginEnabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !enabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "local login is disabled"})
		return
	}
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
		if retry < 1 {
			retry = 1
		}
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
	result, err := h.auth.LoginBearerWithMetadata(r.Context(), in.Username, in.Password, address, r.UserAgent())
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if errors.Is(err, auth.ErrPasswordWorkBusy) {
			slog.Warn("security event", "event", "auth.login_rate_limited", "remote_address", address, "reason", "password_work_capacity")
			writeLoginPasswordWorkBusy(w)
			return
		}
		locked = h.protector.Failure(r.Context(), in.Username, address)
		event := "auth.login_failure"
		if locked {
			event = "auth.login_rate_limited"
		}
		slog.Warn("security event", "event", event, "remote_address", address)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	h.protector.Success(in.Username, address)
	slog.Info("security event", "event", "auth.login_success", "user_id", result.User.ID, "remote_address", address)
	writeJSON(w, http.StatusOK, result)
}
func (h *loginHandler) logout(w http.ResponseWriter, r *http.Request) {
	user, session, ok := managementUserFromRequest(r)
	if ok && session.ID != "" {
		_ = h.auth.RevokeSession(r.Context(), session.ID)
	}
	if ok && user.ID != 0 {
		slog.Info("security event", "event", "session.revoked", "user_id", user.ID, "current", true)
	}
	w.WriteHeader(http.StatusNoContent)
}
