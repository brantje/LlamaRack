package api

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

const oidcStateCookie = "lcm_oidc_state"

type oidcHandler struct {
	oidc     *auth.OIDCManager
	auth     *auth.Service
	settings *settings.Service
	network  *managersecurity.Network
	policyMu sync.Mutex
}

func NewOIDCHandler(oidc *auth.OIDCManager, a *auth.Service, managerSettings *settings.Service, network *managersecurity.Network) http.Handler {
	return &oidcHandler{oidc: oidc, auth: a, settings: managerSettings, network: network}
}

func (h *oidcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/auth/providers" && r.Method == http.MethodGet:
		h.publicProviders(w, r)
	case path == "/api/v1/auth/oidc/exchange" && r.Method == http.MethodPost:
		h.exchange(w, r)
	case path == "/api/v1/auth/ws-ticket" && r.Method == http.MethodPost:
		h.wsTicket(w, r)
	case strings.HasPrefix(path, "/api/v1/auth/oidc/"):
		h.oidcFlow(w, r, strings.TrimPrefix(path, "/api/v1/auth/oidc/"))
	case path == "/api/v1/admin/auth/settings":
		h.authSettings(w, r)
	case path == "/api/v1/admin/auth/providers/test":
		h.providerDraftTest(w, r)
	case path == "/api/v1/admin/auth/providers":
		h.providers(w, r)
	case strings.HasPrefix(path, "/api/v1/admin/auth/providers/"):
		h.provider(w, r, strings.TrimPrefix(path, "/api/v1/admin/auth/providers/"))
	case path == "/api/v1/admin/auth/identities":
		h.identities(w, r)
	case strings.HasPrefix(path, "/api/v1/admin/auth/identities/"):
		h.identity(w, r, strings.TrimPrefix(path, "/api/v1/admin/auth/identities/"))
	case path == "/api/v1/me/identities" && r.Method == http.MethodGet:
		user, _, ok := managementAuthFromRequest(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		items, err := h.oidc.ListIdentities(r.Context(), user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case strings.HasPrefix(path, "/api/v1/me/identities/") && r.Method == http.MethodDelete:
		h.unlinkOwnIdentity(w, r, strings.TrimPrefix(path, "/api/v1/me/identities/"))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *oidcHandler) publicProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.oidc.PublicProviders(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	localLogin, err := h.settings.Bool(r.Context(), settings.LocalLoginEnabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"local_login_enabled": localLogin, "providers": providers})
}

func (h *oidcHandler) writeAuthSettings(w http.ResponseWriter, r *http.Request) {
	local, err := h.settings.Resolve(r.Context(), settings.LocalLoginEnabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	jit, err := h.settings.Resolve(r.Context(), settings.OIDCJITProvisioningEnabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	autoLink, err := h.settings.Resolve(r.Context(), settings.OIDCAutoLinkEnabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	externalURL, err := h.settings.Resolve(r.Context(), settings.ExternalURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	frontendURL, err := h.settings.Resolve(r.Context(), settings.FrontendURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]settings.Value{
		"local_login_enabled":           local,
		"oidc_jit_provisioning_enabled": jit,
		"oidc_auto_link_enabled":        autoLink,
		"external_url":                  externalURL,
		"frontend_url":                  frontendURL,
	})
}

func (h *oidcHandler) authSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.writeAuthSettings(w, r)
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		LocalLoginEnabled          *bool   `json:"local_login_enabled"`
		OIDCJITProvisioningEnabled *bool   `json:"oidc_jit_provisioning_enabled"`
		OIDCAutoLinkEnabled        *bool   `json:"oidc_auto_link_enabled"`
		ExternalURL                *string `json:"external_url"`
		FrontendURL                *string `json:"frontend_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.FrontendURL != nil {
		if err := validateOIDCFrontendURL(*in.FrontendURL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	// Serialize the lockout-sensitive check with provider test/update/delete
	// operations so another request cannot invalidate the last usable OIDC
	// provider between validation and committing local-login=false.
	h.policyMu.Lock()
	defer h.policyMu.Unlock()
	if in.LocalLoginEnabled != nil && !*in.LocalLoginEnabled {
		usable, err := h.oidc.CanDisableLocalLogin(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if !usable {
			writeJSON(w, http.StatusConflict, map[string]string{"error": auth.ErrAuthLockoutRisk.Error()})
			return
		}
	}
	updates := map[string]any{}
	if in.LocalLoginEnabled != nil {
		updates[settings.LocalLoginEnabled] = *in.LocalLoginEnabled
	}
	if in.OIDCJITProvisioningEnabled != nil {
		updates[settings.OIDCJITProvisioningEnabled] = *in.OIDCJITProvisioningEnabled
	}
	if in.OIDCAutoLinkEnabled != nil {
		updates[settings.OIDCAutoLinkEnabled] = *in.OIDCAutoLinkEnabled
	}
	if in.ExternalURL != nil {
		updates[settings.ExternalURL] = *in.ExternalURL
	}
	if in.FrontendURL != nil {
		updates[settings.FrontendURL] = *in.FrontendURL
	}
	for key, value := range updates {
		if _, err := h.settings.Set(r.Context(), key, value); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		slog.Info("security event", "event", "settings.changed", "setting", key)
	}
	h.writeAuthSettings(w, r)
}

func (h *oidcHandler) oidcFlow(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	providerID := parts[0]
	externalURL, err := h.settings.String(r.Context(), settings.ExternalURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	frontendURL, err := h.settings.String(r.Context(), settings.FrontendURL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	switch parts[1] {
	case "start":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		remember, _ := strconv.ParseBool(r.URL.Query().Get("remember"))
		authorizationURL, err := h.oidc.Start(r.Context(), providerID, remember, h.network.EffectiveRemoteAddress(r), r.UserAgent(), externalURL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		parsed, parseErr := url.Parse(authorizationURL)
		if parseErr != nil || parsed.Query().Get("state") == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to initialize OIDC state"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: parsed.Query().Get("state"), Path: "/api/v1/auth/oidc/", HttpOnly: true, Secure: h.network.IsSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: int((10 * time.Minute).Seconds())})
		http.Redirect(w, r, authorizationURL, http.StatusFound)
	case "callback":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		state := r.URL.Query().Get("state")
		stateCookie, cookieErr := r.Cookie(oidcStateCookie)
		http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/api/v1/auth/oidc/", HttpOnly: true, Secure: h.network.IsSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
		if cookieErr != nil || state == "" || subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(state)) != 1 {
			slog.Warn("security event", "event", "auth.oidc_failure", "provider_id", providerID, "error", "browser state mismatch")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC authentication failed"})
			return
		}
		if err := validateOIDCFrontendURL(frontendURL); err != nil {
			slog.Error("OIDC frontend redirect configuration is invalid", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "OIDC authentication failed"})
			return
		}
		redirectURL, err := h.oidc.CompleteCallback(r.Context(), providerID, state, r.URL.Query().Get("code"), externalURL)
		if err != nil {
			slog.Warn("security event", "event", "auth.oidc_failure", "provider_id", providerID, "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "OIDC authentication failed"})
			return
		}
		redirectURL, err = oidcFrontendExchangeURL(redirectURL, frontendURL)
		if err != nil {
			slog.Error("OIDC frontend redirect failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "OIDC authentication failed"})
			return
		}
		slog.Info("security event", "event", "auth.oidc_success", "provider_id", providerID)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *oidcHandler) exchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	result, err := h.oidc.Exchange(in.Code)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired OIDC exchange code"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *oidcHandler) wsTicket(w http.ResponseWriter, r *http.Request) {
	_, session, ok := managementAuthFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	ticket, expiresAt, err := h.auth.IssueWebSocketTicket(r.Context(), session)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "expires_at": expiresAt})
}

func (h *oidcHandler) providerDraftTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in auth.OIDCProviderInput
	if !decode(w, r, &in) {
		return
	}
	if err := h.oidc.TestProviderInput(r.Context(), in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	slog.Info("security event", "event", "auth.oidc_provider_configuration_tested", "success", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *oidcHandler) providers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		providers, err := h.oidc.ListProviders(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, providers)
	case http.MethodPost:
		var in auth.OIDCProviderInput
		if !decode(w, r, &in) {
			return
		}
		provider, err := h.oidc.CreateProvider(r.Context(), in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		slog.Info("security event", "event", "auth.oidc_provider_created", "provider_id", provider.ID)
		writeJSON(w, http.StatusCreated, provider)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *oidcHandler) provider(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) < 1 || parts[0] == "" || len(parts) > 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		if parts[1] != "test" || r.Method != http.MethodPost {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		h.policyMu.Lock()
		defer h.policyMu.Unlock()
		provider, err := h.oidc.TestProvider(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "provider": provider})
			return
		}
		slog.Info("security event", "event", "auth.oidc_provider_tested", "provider_id", id, "success", true)
		writeJSON(w, http.StatusOK, provider)
		return
	}
	switch r.Method {
	case http.MethodGet:
		provider, err := h.oidc.GetProvider(r.Context(), id)
		if err != nil {
			writeOIDCNotFound(w, err, "OIDC provider not found")
			return
		}
		writeJSON(w, http.StatusOK, provider)
	case http.MethodPut, http.MethodPatch:
		h.policyMu.Lock()
		defer h.policyMu.Unlock()
		var in auth.OIDCProviderInput
		if !decode(w, r, &in) {
			return
		}
		provider, err := h.oidc.UpdateProvider(r.Context(), id, in)
		if err != nil {
			if errors.Is(err, auth.ErrAuthLockoutRisk) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeOIDCNotFound(w, err, "OIDC provider not found")
			return
		}
		slog.Info("security event", "event", "auth.oidc_provider_updated", "provider_id", id)
		writeJSON(w, http.StatusOK, provider)
	case http.MethodDelete:
		h.policyMu.Lock()
		defer h.policyMu.Unlock()
		if err := h.oidc.DeleteProvider(r.Context(), id); err != nil {
			if errors.Is(err, auth.ErrAuthLockoutRisk) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeOIDCNotFound(w, err, "OIDC provider not found")
			return
		}
		slog.Info("security event", "event", "auth.oidc_provider_deleted", "provider_id", id)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *oidcHandler) identities(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.oidc.ListIdentities(r.Context(), 0)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var in struct {
			UserID     int64  `json:"user_id"`
			ProviderID string `json:"provider_id"`
			Issuer     string `json:"issuer"`
			Subject    string `json:"subject"`
		}
		if !decode(w, r, &in) {
			return
		}
		identity, err := h.oidc.LinkIdentity(r.Context(), in.UserID, in.ProviderID, in.Issuer, in.Subject)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		slog.Info("security event", "event", "auth.external_identity_linked", "user_id", identity.UserID, "provider_id", identity.ProviderID)
		writeJSON(w, http.StatusCreated, identity)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *oidcHandler) identity(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete || id == "" || strings.Contains(id, "/") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.oidc.UnlinkIdentity(r.Context(), id); err != nil {
		writeOIDCNotFound(w, err, "external identity not found")
		return
	}
	slog.Info("security event", "event", "auth.external_identity_unlinked", "identity_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *oidcHandler) unlinkOwnIdentity(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || strings.Contains(id, "/") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, _, ok := managementAuthFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if err := h.oidc.UnlinkOwnIdentity(r.Context(), user.ID, id); err != nil {
		writeOIDCNotFound(w, err, "external identity not found")
		return
	}
	slog.Info("security event", "event", "auth.external_identity_unlinked", "identity_id", id, "user_id", user.ID)
	w.WriteHeader(http.StatusNoContent)
}

func writeOIDCNotFound(w http.ResponseWriter, err error, message string) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": message})
		return
	}
	writeErr(w, http.StatusBadRequest, err)
}
