package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/litellm"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

type adminHandler struct {
	auth     *auth.Service
	settings *settings.Service
	secrets  *huggingface.SecretStore
	litellm  *litellm.Service
	network  *managersecurity.Network
	profile  func() (llamacpp.Profile, error)
	started  time.Time
}

func NewAdminHandler(a *auth.Service, managerSettings *settings.Service, secrets *huggingface.SecretStore, network *managersecurity.Network, profile func() (llamacpp.Profile, error), liteLLMServices ...*litellm.Service) http.Handler {
	var liteLLM *litellm.Service
	if len(liteLLMServices) > 0 {
		liteLLM = liteLLMServices[0]
	}
	return &adminHandler{auth: a, settings: managerSettings, secrets: secrets, litellm: liteLLM, network: network, profile: profile, started: time.Now()}
}

func (h *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	user, session, hasUser := principal.UserSession()
	switch {
	case path == "/api/v1/me" && r.Method == http.MethodGet:
		if !hasUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
			return
		}
		writeJSON(w, http.StatusOK, user)
	case path == "/api/v1/me/password" && r.Method == http.MethodPost:
		if !hasUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
			return
		}
		h.changePassword(w, r, user, session)
	case path == "/api/v1/me/sessions" && r.Method == http.MethodGet:
		if !hasUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
			return
		}
		h.listSessions(w, r, user.ID, session.ID)
	case strings.HasPrefix(path, "/api/v1/me/sessions/") && r.Method == http.MethodDelete:
		if !hasUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
			return
		}
		h.revokeOwnSession(w, r, user, strings.TrimPrefix(path, "/api/v1/me/sessions/"))
	case path == "/api/v1/me/sessions/revoke-others" && r.Method == http.MethodPost:
		if !hasUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
			return
		}
		count, err := h.auth.RevokeOtherSessions(r.Context(), user.ID, session.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("security event", "event", "session.all_revoked", "user_id", user.ID, "except_current", true, "count", count)
		writeJSON(w, http.StatusOK, map[string]int64{"revoked": count})
	case path == "/api/v1/me/sessions/revoke-all" && r.Method == http.MethodPost:
		if !hasUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this API key cannot access this endpoint"})
			return
		}
		count, err := h.auth.RevokeAllSessions(r.Context(), user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("security event", "event", "session.all_revoked", "user_id", user.ID, "except_current", false, "count", count)
		w.WriteHeader(http.StatusNoContent)
	case path == "/api/v1/users":
		h.users(w, r, principal)
	case strings.HasPrefix(path, "/api/v1/users/"):
		h.userRoute(w, r, principal, session, strings.TrimPrefix(path, "/api/v1/users/"))
	case strings.HasPrefix(path, "/api/v1/sessions/"):
		h.sessionRoute(w, r, principal, strings.TrimPrefix(path, "/api/v1/sessions/"))
	case path == "/api/v1/settings/general":
		h.generalSettings(w, r, principal)
	case path == "/api/v1/system" && r.Method == http.MethodGet:
		h.system(w, r)
	case path == "/api/v1/admin/summary" && r.Method == http.MethodGet:
		h.summary(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *adminHandler) current(r *http.Request) (managementAuthContext, bool) {
	if principal, ok := managementAuthFromRequest(r); ok {
		return principal, true
	}
	cookie := sessionCookieValue(r)
	if cookie == "" {
		return managementAuthContext{}, false
	}
	user, session, err := h.auth.SessionUserWithSession(r.Context(), cookie)
	if err != nil {
		return managementAuthContext{}, false
	}
	return managementAuthContext{User: &user, Session: &session}, true
}

func (h *adminHandler) users(w http.ResponseWriter, r *http.Request, principal managementAuthContext) {
	switch r.Method {
	case http.MethodGet:
		users, err := h.auth.ListUsers(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, users)
	case http.MethodPost:
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		created, err := h.auth.CreateUser(r.Context(), in.Username, in.Password)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", "user.created", "target_user_id", created.ID)...)
		writeJSON(w, http.StatusCreated, created)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *adminHandler) userRoute(w http.ResponseWriter, r *http.Request, principal managementAuthContext, currentSession auth.Session, rest string) {
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var in struct {
				Enabled *bool `json:"enabled"`
			}
			if !decode(w, r, &in) {
				return
			}
			if in.Enabled == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled is required"})
				return
			}
			if err := h.auth.SetUserEnabled(r.Context(), id, *in.Enabled); err != nil {
				writeUserMutationError(w, err)
				return
			}
			event := "user.disabled"
			if *in.Enabled {
				event = "user.enabled"
			}
			slog.Info("security event", append(actorLogAttrs(principal), "event", event, "target_user_id", id)...)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := h.auth.DeleteUser(r.Context(), principal.CreatedByUserID(), id); err != nil {
				writeUserMutationError(w, err)
				return
			}
			slog.Info("security event", append(actorLogAttrs(principal), "event", "user.deleted", "target_user_id", id)...)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	switch parts[1] {
	case "password":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := h.auth.ResetPassword(r.Context(), id, in.Password); err != nil {
			writeUserMutationError(w, err)
			return
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", "user.password_reset", "target_user_id", id)...)
		w.WriteHeader(http.StatusNoContent)
	case "sessions":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		currentID := ""
		if principal.User != nil && principal.User.ID == id {
			currentID = currentSession.ID
		}
		h.listSessions(w, r, id, currentID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *adminHandler) changePassword(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session) {
	var in struct {
		CurrentPassword         string `json:"current_password"`
		NewPassword             string `json:"new_password"`
		NewPasswordConfirmation string `json:"new_password_confirmation"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.NewPassword != in.NewPasswordConfirmation {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password confirmation does not match"})
		return
	}
	if err := h.auth.ChangePassword(r.Context(), user.ID, in.CurrentPassword, in.NewPassword, session.ID); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current password is invalid"})
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	slog.Info("security event", "event", "user.password_changed", "user_id", user.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) listSessions(w http.ResponseWriter, r *http.Request, userID int64, currentSessionID string) {
	items, err := h.auth.ListSessions(r.Context(), userID, currentSessionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *adminHandler) sessionRoute(w http.ResponseWriter, r *http.Request, principal managementAuthContext, id string) {
	if r.Method != http.MethodDelete || strings.Contains(id, "/") || id == "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.auth.RevokeSession(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("security event", append(actorLogAttrs(principal), "event", "session.revoked", "session_id", id)...)
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) revokeOwnSession(w http.ResponseWriter, r *http.Request, actor auth.User, id string) {
	if strings.Contains(id, "/") || id == "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.auth.RevokeOwnSession(r.Context(), actor.ID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("security event", "event", "session.revoked", "actor_user_id", actor.ID, "session_id", id)
	w.WriteHeader(http.StatusNoContent)
}

type generalSettingsInput struct {
	SessionLifetimeSeconds     *int    `json:"session_lifetime_seconds"`
	LoginProtectionEnabled     *bool   `json:"login_protection_enabled"`
	LoginFailureThreshold      *int    `json:"login_failure_threshold"`
	LoginLockoutSeconds        *int    `json:"login_lockout_seconds"`
	TrustedProxies             *string `json:"trusted_proxies"`
	AllowedOrigins             *string `json:"allowed_origins"`
	ExternalURL                *string `json:"external_url"`
	StartupTimeoutSeconds      *int    `json:"startup_timeout_seconds"`
	IdleUnloadSeconds          *int    `json:"idle_unload_seconds"`
	AlwaysOnReconcileSeconds   *int    `json:"always_on_reconcile_seconds"`
	MaxPendingPerInstance      *int    `json:"max_pending_requests_per_instance"`
	MaxPendingGlobal           *int    `json:"max_pending_requests_global"`
	ObservabilityRetentionDays *int    `json:"observability_retention_days"`
	PrometheusAuthToken        *string `json:"prometheus_auth_token"`
}

type generalSettingClass uint8

const (
	generalSettingSensitive generalSettingClass = iota
	generalSettingOperational
)

type generalSettingDefinition struct {
	key   string
	class generalSettingClass
	read  func() (any, bool)
}

type generalSettingUpdate struct {
	key   string
	value any
	class generalSettingClass
}

func generalSettingValue[T any](value *T) func() (any, bool) {
	return func() (any, bool) {
		if value == nil {
			return nil, false
		}
		return *value, true
	}
}

func generalSettingsUpdates(in generalSettingsInput) []generalSettingUpdate {
	definitions := []generalSettingDefinition{
		{key: settings.SessionLifetimeSeconds, class: generalSettingSensitive, read: generalSettingValue(in.SessionLifetimeSeconds)},
		{key: settings.LoginProtectionEnabled, class: generalSettingSensitive, read: generalSettingValue(in.LoginProtectionEnabled)},
		{key: settings.LoginFailureThreshold, class: generalSettingSensitive, read: generalSettingValue(in.LoginFailureThreshold)},
		{key: settings.LoginLockoutSeconds, class: generalSettingSensitive, read: generalSettingValue(in.LoginLockoutSeconds)},
		{key: settings.TrustedProxies, class: generalSettingSensitive, read: generalSettingValue(in.TrustedProxies)},
		{key: settings.AllowedOrigins, class: generalSettingSensitive, read: generalSettingValue(in.AllowedOrigins)},
		{key: settings.ExternalURL, class: generalSettingSensitive, read: generalSettingValue(in.ExternalURL)},
		{key: settings.StartupTimeoutSeconds, class: generalSettingOperational, read: generalSettingValue(in.StartupTimeoutSeconds)},
		{key: settings.IdleUnloadSeconds, class: generalSettingOperational, read: generalSettingValue(in.IdleUnloadSeconds)},
		{key: settings.AlwaysOnReconcileSeconds, class: generalSettingOperational, read: generalSettingValue(in.AlwaysOnReconcileSeconds)},
		{key: settings.MaxPendingRequestsPerInstance, class: generalSettingOperational, read: generalSettingValue(in.MaxPendingPerInstance)},
		{key: settings.MaxPendingRequestsGlobal, class: generalSettingOperational, read: generalSettingValue(in.MaxPendingGlobal)},
		{key: settings.ObservabilityRetentionDays, class: generalSettingOperational, read: generalSettingValue(in.ObservabilityRetentionDays)},
		{key: settings.PrometheusAuthToken, class: generalSettingSensitive, read: generalSettingValue(in.PrometheusAuthToken)},
	}

	updates := make([]generalSettingUpdate, 0, len(definitions))
	for _, definition := range definitions {
		value, present := definition.read()
		if !present {
			continue
		}
		updates = append(updates, generalSettingUpdate{key: definition.key, value: value, class: definition.class})
	}
	return updates
}

func generalSettingsRequireUserPrincipal(updates []generalSettingUpdate) bool {
	for _, update := range updates {
		// Unknown/zero-value classifications are security-sensitive by default.
		if update.class != generalSettingOperational {
			return true
		}
	}
	return false
}

func (h *adminHandler) generalSettings(w http.ResponseWriter, r *http.Request, principal managementAuthContext) {
	if r.Method == http.MethodGet {
		general, err := h.settings.General(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, general)
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in generalSettingsInput
	if !decode(w, r, &in) {
		return
	}
	updates := generalSettingsUpdates(in)
	if generalSettingsRequireUserPrincipal(updates) && !requireManagementUserPrincipal(w, principal) {
		return
	}
	for _, update := range updates {
		if _, err := h.settings.Set(r.Context(), update.key, update.value); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if update.key == settings.SessionLifetimeSeconds {
			if seconds, ok := update.value.(int); ok {
				h.auth.SetSessionLifetime(time.Duration(seconds) * time.Second)
			}
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", "settings.changed", "setting", update.key)...)
	}
	general, err := h.settings.General(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, general)
}

func (h *adminHandler) summary(w http.ResponseWriter, r *http.Request) {
	users, err := h.auth.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	enabled := 0
	for _, user := range users {
		if user.Enabled {
			enabled++
		}
	}
	hfStatus, err := h.secrets.TokenStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	liteLLM := map[string]any{"configured": false}
	if h.litellm != nil {
		if status, statusErr := h.litellm.Status(r.Context()); statusErr == nil {
			liteLLM["configured"] = status.Configured
			if status.LastSync != nil {
				liteLLM["last_sync_ok"] = status.LastSyncOK
			}
		}
	}
	profile, profileErr := h.profile()
	llama := map[string]any{"available": profileErr == nil}
	if profileErr == nil {
		llama["path"] = profile.Path
		llama["version"] = profile.Version
		llama["fingerprint"] = profile.Fingerprint
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":       map[string]int{"total": len(users), "enabled": enabled},
		"huggingface": hfStatus,
		"litellm":     liteLLM,
		"llamacpp":    llama,
	})
}

func (h *adminHandler) system(w http.ResponseWriter, r *http.Request) {
	general, err := h.settings.General(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	profile, profileErr := h.profile()
	llama := map[string]any{"available": profileErr == nil}
	if profileErr == nil {
		llama["path"] = profile.Path
		llama["version"] = profile.Version
		llama["fingerprint"] = profile.Fingerprint
		llama["options"] = len(profile.Options)
	}
	forwarding := h.network.RequestForwardingDiagnostics(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"manager": map[string]any{"uptime_seconds": int64(time.Since(h.started).Seconds()), "runtime": general.Runtime},
		"network": map[string]any{
			"effective_scheme": h.network.EffectiveScheme(r), "secure_cookie": h.network.IsSecure(r),
			"allowed_origins": general.AllowedOrigins, "trusted_proxies": general.TrustedProxies, "external_url": general.ExternalURL,
			"request_forwarding": map[string]any{
				"peer_address": forwarding.PeerAddress, "peer_trusted": forwarding.PeerTrusted,
				"forwarded_header": forwarding.ForwardedHeader, "x_forwarded_for": forwarding.XForwardedFor,
				"effective_remote_address": forwarding.EffectiveRemoteAddress,
			},
		},
		"llamacpp": llama,
	})
}

func writeUserMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
	case errors.Is(err, auth.ErrLastEnabledUser), errors.Is(err, auth.ErrSelfDelete):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeErr(w, http.StatusBadRequest, err)
	}
}
