package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/settings"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcTransactionLifetime = 10 * time.Minute
	oidcExchangeLifetime    = time.Minute
)

type ProviderSecretStore interface {
	GetSecret(context.Context, string) (string, error)
	SetSecret(context.Context, string, string) error
	DeleteSecret(context.Context, string) error
	SecretConfigured(context.Context, string) (bool, error)
}

type OIDCProvider struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Enabled               bool     `json:"enabled"`
	Issuer                string   `json:"issuer"`
	DiscoveryURL          string   `json:"discovery_url,omitempty"`
	ClientID              string   `json:"client_id"`
	Scopes                []string `json:"scopes"`
	UsernameClaim         string   `json:"username_claim"`
	AuthorizationEndpoint string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string   `json:"token_endpoint,omitempty"`
	JWKSURL               string   `json:"jwks_url,omitempty"`
	SecretConfigured      bool     `json:"secret_configured"`
	LastTestedAt          *int64   `json:"last_tested_at,omitempty"`
	LastTestSucceeded     bool     `json:"last_test_succeeded"`
	CreatedAt             int64    `json:"created_at"`
	UpdatedAt             int64    `json:"updated_at"`
}

type OIDCProviderInput struct {
	Name                  string   `json:"name"`
	Enabled               bool     `json:"enabled"`
	Issuer                string   `json:"issuer"`
	DiscoveryURL          string   `json:"discovery_url"`
	ClientID              string   `json:"client_id"`
	ClientSecret          *string  `json:"client_secret,omitempty"`
	Scopes                []string `json:"scopes"`
	UsernameClaim         string   `json:"username_claim"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURL               string   `json:"jwks_url"`
}

type PublicOIDCProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ExternalIdentity struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	Issuer     string `json:"issuer"`
	Subject    string `json:"subject"`
	UserID     int64  `json:"user_id"`
	CreatedAt  int64  `json:"created_at"`
}

type OIDCExchangeResult struct {
	LoginResult
	Remember bool `json:"remember"`
}

type oidcTransaction struct {
	ProviderID string
	Nonce      string
	Verifier   string
	Remember   bool
	RemoteAddr string
	UserAgent  string
	ExpiresAt  time.Time
}

type oidcExchange struct {
	Result    OIDCExchangeResult
	ExpiresAt time.Time
}

type resolvedOIDCProvider struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	JWKSURL               string
}

type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURL               string `json:"jwks_uri"`
}

type OIDCManager struct {
	auth     *Service
	settings *settings.Service
	secrets  ProviderSecretStore
	client   *http.Client

	mu           sync.Mutex
	transactions map[string]oidcTransaction
	exchanges    map[string]oidcExchange
}

func NewOIDCManager(a *Service, managerSettings *settings.Service, secrets ProviderSecretStore) *OIDCManager {
	return &OIDCManager{
		auth: a, settings: managerSettings, secrets: secrets,
		client:       &http.Client{Timeout: 10 * time.Second},
		transactions: map[string]oidcTransaction{}, exchanges: map[string]oidcExchange{},
	}
}

func oidcSecretName(providerID string) string { return "oidc_provider:" + providerID + ":client_secret" }

func normalizeScopes(scopes []string) []string {
	seen := map[string]bool{"openid": true}
	out := []string{"openid"}
	for _, scope := range scopes {
		for _, value := range strings.Fields(strings.ReplaceAll(scope, ",", " ")) {
			if value = strings.TrimSpace(value); value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func validateProviderInput(in OIDCProviderInput) (OIDCProviderInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Issuer = strings.TrimRight(strings.TrimSpace(in.Issuer), "/")
	in.DiscoveryURL = strings.TrimSpace(in.DiscoveryURL)
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.UsernameClaim = strings.TrimSpace(in.UsernameClaim)
	in.AuthorizationEndpoint = strings.TrimSpace(in.AuthorizationEndpoint)
	in.TokenEndpoint = strings.TrimSpace(in.TokenEndpoint)
	in.JWKSURL = strings.TrimSpace(in.JWKSURL)
	if in.Name == "" || in.Issuer == "" || in.ClientID == "" {
		return OIDCProviderInput{}, errors.New("name, issuer and client_id are required")
	}
	parsed, err := url.Parse(in.Issuer)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return OIDCProviderInput{}, errors.New("issuer must be an absolute HTTP(S) URL")
	}
	if in.UsernameClaim == "" {
		in.UsernameClaim = "preferred_username"
	}
	in.Scopes = normalizeScopes(in.Scopes)
	return in, nil
}

func (m *OIDCManager) CreateProvider(ctx context.Context, in OIDCProviderInput) (OIDCProvider, error) {
	in, err := validateProviderInput(in)
	if err != nil {
		return OIDCProvider{}, err
	}
	if in.ClientSecret == nil || strings.TrimSpace(*in.ClientSecret) == "" {
		return OIDCProvider{}, errors.New("client_secret is required")
	}
	id, err := randomToken(12)
	if err != nil {
		return OIDCProvider{}, err
	}
	now := time.Now().Unix()
	scopes, _ := json.Marshal(in.Scopes)
	_, err = m.auth.db.ExecContext(ctx, `INSERT INTO oidc_providers(id,name,enabled,issuer,discovery_url,client_id,scopes,username_claim,authorization_endpoint,token_endpoint,jwks_url,last_test_succeeded,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, in.Name, boolInt(in.Enabled), in.Issuer, in.DiscoveryURL, in.ClientID, string(scopes), in.UsernameClaim, in.AuthorizationEndpoint, in.TokenEndpoint, in.JWKSURL, 0, now, now)
	if err != nil {
		return OIDCProvider{}, err
	}
	if err := m.secrets.SetSecret(ctx, oidcSecretName(id), strings.TrimSpace(*in.ClientSecret)); err != nil {
		_, _ = m.auth.db.ExecContext(ctx, "DELETE FROM oidc_providers WHERE id=?", id)
		return OIDCProvider{}, err
	}
	return m.GetProvider(ctx, id)
}

func (m *OIDCManager) UpdateProvider(ctx context.Context, id string, in OIDCProviderInput) (OIDCProvider, error) {
	current, err := m.GetProvider(ctx, id)
	if err != nil {
		return OIDCProvider{}, err
	}
	in, err = validateProviderInput(in)
	if err != nil {
		return OIDCProvider{}, err
	}
	changed := current.Name != in.Name || current.Issuer != in.Issuer || current.DiscoveryURL != in.DiscoveryURL || current.ClientID != in.ClientID || current.UsernameClaim != in.UsernameClaim || current.AuthorizationEndpoint != in.AuthorizationEndpoint || current.TokenEndpoint != in.TokenEndpoint || current.JWKSURL != in.JWKSURL || !stringSlicesEqual(current.Scopes, in.Scopes) || in.ClientSecret != nil
	if (changed && current.LastTestSucceeded) || (!in.Enabled && current.Enabled) {
		local, resolveErr := m.settings.Bool(ctx, settings.LocalLoginEnabled)
		if resolveErr != nil {
			return OIDCProvider{}, resolveErr
		}
		if !local {
			other, resolveErr := m.hasUsableProvider(ctx, id)
			if resolveErr != nil {
				return OIDCProvider{}, resolveErr
			}
			if !other {
				return OIDCProvider{}, ErrAuthLockoutRisk
			}
		}
	}
	scopes, _ := json.Marshal(in.Scopes)
	tested, testedAt := current.LastTestSucceeded, any(nil)
	if current.LastTestedAt != nil {
		testedAt = *current.LastTestedAt
	}
	if changed || (!in.Enabled && current.Enabled) {
		tested, testedAt = false, nil
	}
	_, err = m.auth.db.ExecContext(ctx, `UPDATE oidc_providers SET name=?,enabled=?,issuer=?,discovery_url=?,client_id=?,scopes=?,username_claim=?,authorization_endpoint=?,token_endpoint=?,jwks_url=?,last_tested_at=?,last_test_succeeded=?,updated_at=? WHERE id=?`, in.Name, boolInt(in.Enabled), in.Issuer, in.DiscoveryURL, in.ClientID, string(scopes), in.UsernameClaim, in.AuthorizationEndpoint, in.TokenEndpoint, in.JWKSURL, testedAt, boolInt(tested), time.Now().Unix(), id)
	if err != nil {
		return OIDCProvider{}, err
	}
	if in.ClientSecret != nil && strings.TrimSpace(*in.ClientSecret) != "" {
		if err := m.secrets.SetSecret(ctx, oidcSecretName(id), strings.TrimSpace(*in.ClientSecret)); err != nil {
			return OIDCProvider{}, err
		}
	}
	return m.GetProvider(ctx, id)
}

func (m *OIDCManager) DeleteProvider(ctx context.Context, id string) error {
	if _, err := m.GetProvider(ctx, id); err != nil {
		return err
	}
	if err := m.ensureProviderMayBeDisabled(ctx, id); err != nil {
		return err
	}
	if _, err := m.auth.db.ExecContext(ctx, "DELETE FROM oidc_providers WHERE id=?", id); err != nil {
		return err
	}
	return m.secrets.DeleteSecret(ctx, oidcSecretName(id))
}

func (m *OIDCManager) GetProvider(ctx context.Context, id string) (OIDCProvider, error) {
	provider, err := scanOIDCProvider(m.auth.db.QueryRowContext(ctx, `SELECT id,name,enabled,issuer,discovery_url,client_id,scopes,username_claim,authorization_endpoint,token_endpoint,jwks_url,last_tested_at,last_test_succeeded,created_at,updated_at FROM oidc_providers WHERE id=?`, id).Scan)
	if err != nil {
		return OIDCProvider{}, err
	}
	provider.SecretConfigured, err = m.secrets.SecretConfigured(ctx, oidcSecretName(id))
	return provider, err
}

func (m *OIDCManager) ListProviders(ctx context.Context) ([]OIDCProvider, error) {
	rows, err := m.auth.db.QueryContext(ctx, `SELECT id,name,enabled,issuer,discovery_url,client_id,scopes,username_claim,authorization_endpoint,token_endpoint,jwks_url,last_tested_at,last_test_succeeded,created_at,updated_at FROM oidc_providers ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OIDCProvider, 0)
	for rows.Next() {
		provider, err := scanOIDCProvider(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		configured, err := m.secrets.SecretConfigured(ctx, oidcSecretName(out[i].ID))
		if err != nil {
			return nil, err
		}
		out[i].SecretConfigured = configured
	}
	return out, nil
}

type scanFunc func(...any) error

func scanOIDCProvider(scan scanFunc) (OIDCProvider, error) {
	var provider OIDCProvider
	var enabled, tested int
	var scopesRaw string
	var testedAt sql.NullInt64
	if err := scan(&provider.ID, &provider.Name, &enabled, &provider.Issuer, &provider.DiscoveryURL, &provider.ClientID, &scopesRaw, &provider.UsernameClaim, &provider.AuthorizationEndpoint, &provider.TokenEndpoint, &provider.JWKSURL, &testedAt, &tested, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
		return OIDCProvider{}, err
	}
	provider.Enabled, provider.LastTestSucceeded = enabled != 0, tested != 0
	if testedAt.Valid {
		v := testedAt.Int64
		provider.LastTestedAt = &v
	}
	if err := json.Unmarshal([]byte(scopesRaw), &provider.Scopes); err != nil || len(provider.Scopes) == 0 {
		provider.Scopes = []string{"openid"}
	}
	return provider, nil
}

func (m *OIDCManager) PublicProviders(ctx context.Context) ([]PublicOIDCProvider, error) {
	rows, err := m.auth.db.QueryContext(ctx, `SELECT id,name FROM oidc_providers WHERE enabled=1 ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicOIDCProvider, 0)
	for rows.Next() {
		var item PublicOIDCProvider
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (m *OIDCManager) TestProvider(ctx context.Context, id string) (OIDCProvider, error) {
	provider, err := m.GetProvider(ctx, id)
	if err != nil {
		return OIDCProvider{}, err
	}
	resolved, testErr := m.resolveProvider(ctx, provider)
	if testErr == nil {
		testErr = m.probeJWKS(ctx, resolved.JWKSURL)
	}
	configured, secretErr := m.secrets.SecretConfigured(ctx, oidcSecretName(id))
	if testErr == nil && secretErr != nil {
		testErr = secretErr
	} else if testErr == nil && !configured {
		testErr = errors.New("client secret is not configured")
	}
	now := time.Now().Unix()
	if _, err := m.auth.db.ExecContext(ctx, "UPDATE oidc_providers SET last_tested_at=?,last_test_succeeded=?,updated_at=? WHERE id=?", now, boolInt(testErr == nil), now, id); err != nil {
		return OIDCProvider{}, err
	}
	updated, err := m.GetProvider(ctx, id)
	if err != nil {
		return OIDCProvider{}, err
	}
	return updated, testErr
}

func (m *OIDCManager) probeJWKS(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OIDC JWKS endpoint returned HTTP %d", resp.StatusCode)
	}
	var doc struct{ Keys []json.RawMessage `json:"keys"` }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("decode OIDC JWKS: %w", err)
	}
	if len(doc.Keys) == 0 {
		return errors.New("OIDC JWKS endpoint returned no keys")
	}
	return nil
}

func (m *OIDCManager) CanDisableLocalLogin(ctx context.Context) (bool, error) { return m.hasUsableProvider(ctx, "") }

func (m *OIDCManager) hasUsableProvider(ctx context.Context, excludeID string) (bool, error) {
	query, args := "SELECT COUNT(*) FROM oidc_providers WHERE enabled=1 AND last_test_succeeded=1", []any{}
	if excludeID != "" {
		query += " AND id<>?"
		args = append(args, excludeID)
	}
	var count int
	if err := m.auth.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (m *OIDCManager) ensureProviderMayBeDisabled(ctx context.Context, id string) error {
	local, err := m.settings.Bool(ctx, settings.LocalLoginEnabled)
	if err != nil || local {
		return err
	}
	usable, err := m.hasUsableProvider(ctx, id)
	if err != nil {
		return err
	}
	if !usable {
		return ErrAuthLockoutRisk
	}
	return nil
}

func (m *OIDCManager) resolveProvider(ctx context.Context, provider OIDCProvider) (resolvedOIDCProvider, error) {
	resolved := resolvedOIDCProvider{Issuer: provider.Issuer, AuthorizationEndpoint: provider.AuthorizationEndpoint, TokenEndpoint: provider.TokenEndpoint, JWKSURL: provider.JWKSURL}
	if resolved.AuthorizationEndpoint == "" || resolved.TokenEndpoint == "" || resolved.JWKSURL == "" {
		discoveryURL := provider.DiscoveryURL
		if discoveryURL == "" {
			discoveryURL = strings.TrimRight(provider.Issuer, "/") + "/.well-known/openid-configuration"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
		if err != nil {
			return resolvedOIDCProvider{}, err
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return resolvedOIDCProvider{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resolvedOIDCProvider{}, fmt.Errorf("OIDC discovery returned HTTP %d", resp.StatusCode)
		}
		var document discoveryDocument
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&document); err != nil {
			return resolvedOIDCProvider{}, fmt.Errorf("decode OIDC discovery: %w", err)
		}
		if strings.TrimSuffix(document.Issuer, "/") != strings.TrimSuffix(provider.Issuer, "/") {
			return resolvedOIDCProvider{}, errors.New("OIDC discovery issuer does not match configured issuer")
		}
		// OIDC issuer validation is exact. If discovery differs only by the
		// optional trailing slash, trust the discovery document's canonical
		// issuer so ID-token verification uses the exact value emitted by the IdP.
		resolved.Issuer = document.Issuer
		if resolved.AuthorizationEndpoint == "" { resolved.AuthorizationEndpoint = document.AuthorizationEndpoint }
		if resolved.TokenEndpoint == "" { resolved.TokenEndpoint = document.TokenEndpoint }
		if resolved.JWKSURL == "" { resolved.JWKSURL = document.JWKSURL }
	}
	for name, value := range map[string]string{"authorization endpoint": resolved.AuthorizationEndpoint, "token endpoint": resolved.TokenEndpoint, "JWKS URL": resolved.JWKSURL} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return resolvedOIDCProvider{}, fmt.Errorf("%s is invalid", name)
		}
	}
	return resolved, nil
}

func (m *OIDCManager) Start(ctx context.Context, providerID string, remember bool, remoteAddress, userAgent, externalURL string) (string, error) {
	provider, err := m.GetProvider(ctx, providerID)
	if err != nil { return "", err }
	if !provider.Enabled { return "", errors.New("OIDC provider is disabled") }
	resolved, err := m.resolveProvider(ctx, provider)
	if err != nil { return "", err }
	secret, err := m.secrets.GetSecret(ctx, oidcSecretName(provider.ID))
	if err != nil || secret == "" { return "", errors.New("OIDC provider client secret is unavailable") }
	redirectURL, err := oidcCallbackURL(externalURL, provider.ID)
	if err != nil { return "", err }
	state, err := randomToken(24); if err != nil { return "", err }
	nonce, err := randomToken(24); if err != nil { return "", err }
	verifier, err := randomToken(32); if err != nil { return "", err }
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	config := oauth2.Config{ClientID: provider.ClientID, ClientSecret: secret, RedirectURL: redirectURL, Scopes: provider.Scopes, Endpoint: oauth2.Endpoint{AuthURL: resolved.AuthorizationEndpoint, TokenURL: resolved.TokenEndpoint}}
	authURL := config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	m.mu.Lock()
	m.cleanupLocked(time.Now())
	m.transactions[state] = oidcTransaction{ProviderID: provider.ID, Nonce: nonce, Verifier: verifier, Remember: remember, RemoteAddr: remoteAddress, UserAgent: truncate(userAgent, 512), ExpiresAt: time.Now().Add(oidcTransactionLifetime)}
	m.mu.Unlock()
	return authURL, nil
}

func (m *OIDCManager) CompleteCallback(ctx context.Context, providerID, state, code, externalURL string) (string, error) {
	m.mu.Lock()
	transaction, ok := m.transactions[state]
	delete(m.transactions, state)
	m.cleanupLocked(time.Now())
	m.mu.Unlock()
	if !ok || !transaction.ExpiresAt.After(time.Now()) || transaction.ProviderID != providerID || strings.TrimSpace(code) == "" {
		return "", errors.New("invalid or expired OIDC state")
	}
	provider, err := m.GetProvider(ctx, providerID)
	if err != nil || !provider.Enabled { return "", errors.New("OIDC provider is unavailable") }
	resolved, err := m.resolveProvider(ctx, provider)
	if err != nil { return "", err }
	secret, err := m.secrets.GetSecret(ctx, oidcSecretName(provider.ID))
	if err != nil || secret == "" { return "", errors.New("OIDC provider client secret is unavailable") }
	redirectURL, err := oidcCallbackURL(externalURL, provider.ID)
	if err != nil { return "", err }
	ctx = context.WithValue(ctx, oauth2.HTTPClient, m.client)
	config := oauth2.Config{ClientID: provider.ClientID, ClientSecret: secret, RedirectURL: redirectURL, Scopes: provider.Scopes, Endpoint: oauth2.Endpoint{AuthURL: resolved.AuthorizationEndpoint, TokenURL: resolved.TokenEndpoint}}
	token, err := config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", transaction.Verifier))
	if err != nil { return "", fmt.Errorf("OIDC code exchange failed: %w", err) }
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" { return "", errors.New("OIDC token response did not include id_token") }
	verifier := oidc.NewVerifier(resolved.Issuer, oidc.NewRemoteKeySet(ctx, resolved.JWKSURL), &oidc.Config{ClientID: provider.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil { return "", fmt.Errorf("verify OIDC ID token: %w", err) }
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil { return "", err }
	nonce, _ := claims["nonce"].(string)
	if nonce == "" || nonce != transaction.Nonce { return "", errors.New("OIDC nonce validation failed") }
	user, err := m.resolveIdentity(ctx, provider, idToken.Subject, oidcUsername(provider, claims, idToken.Subject))
	if err != nil { return "", err }
	result, err := m.auth.CreateBearerSession(ctx, user, transaction.RemoteAddr, transaction.UserAgent)
	if err != nil { return "", err }
	now := time.Now().Unix()
	if _, err := m.auth.db.ExecContext(ctx, "UPDATE users SET last_login_at=? WHERE id=?", now, user.ID); err == nil { result.User.LastLoginAt = &now }
	exchangeCode, err := randomToken(24)
	if err != nil {
		_ = m.auth.RevokeSession(ctx, resultSessionID(result.AccessToken, m.auth))
		return "", err
	}
	m.mu.Lock()
	m.cleanupLocked(time.Now())
	m.exchanges[exchangeCode] = oidcExchange{Result: OIDCExchangeResult{LoginResult: result, Remember: transaction.Remember}, ExpiresAt: time.Now().Add(oidcExchangeLifetime)}
	m.mu.Unlock()
	return frontendExchangeURL(externalURL, exchangeCode)
}

func resultSessionID(token string, service *Service) string {
	claims, err := service.parseManagementToken(token)
	if err != nil { return "" }
	return claims.SessionID
}

func (m *OIDCManager) Exchange(code string) (OIDCExchangeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(time.Now())
	code = strings.TrimSpace(code)
	item, ok := m.exchanges[code]
	delete(m.exchanges, code)
	if !ok || !item.ExpiresAt.After(time.Now()) { return OIDCExchangeResult{}, errors.New("invalid or expired OIDC exchange code") }
	return item.Result, nil
}

func (m *OIDCManager) cleanupLocked(now time.Time) {
	for key, item := range m.transactions { if !item.ExpiresAt.After(now) { delete(m.transactions, key) } }
	for key, item := range m.exchanges { if !item.ExpiresAt.After(now) { delete(m.exchanges, key) } }
}

func oidcCallbackURL(externalURL, providerID string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(externalURL))
	if err != nil || base.Scheme == "" || base.Host == "" { return "", errors.New("external/public URL must be configured before OIDC can be used") }
	base.Path = strings.TrimRight(base.Path, "/") + "/api/v1/auth/oidc/" + url.PathEscape(providerID) + "/callback"
	base.RawQuery, base.Fragment = "", ""
	return base.String(), nil
}

func frontendExchangeURL(externalURL, code string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(externalURL))
	if err != nil || base.Scheme == "" || base.Host == "" { return "", errors.New("external/public URL must be configured before OIDC can be used") }
	query := base.Query(); query.Set("oidc_exchange", code); base.RawQuery, base.Fragment = query.Encode(), ""
	return base.String(), nil
}

func oidcUsername(provider OIDCProvider, claims map[string]any, subject string) string {
	seen := map[string]bool{}
	for _, key := range []string{provider.UsernameClaim, "preferred_username", "email", "name"} {
		if key == "" || seen[key] { continue }
		seen[key] = true
		value, _ := claims[key].(string)
		if value = strings.TrimSpace(value); value != "" { return value }
	}
	return "oidc-" + subject
}

func (m *OIDCManager) resolveIdentity(ctx context.Context, provider OIDCProvider, subject, username string) (User, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" { return User{}, errors.New("OIDC subject is missing") }
	var userID int64
	err := m.auth.db.QueryRowContext(ctx, "SELECT user_id FROM external_identities WHERE provider_id=? AND issuer=? AND subject=?", provider.ID, provider.Issuer, subject).Scan(&userID)
	if err == nil {
		user, err := m.auth.UserByID(ctx, userID)
		if err != nil || !user.Enabled { return User{}, ErrSessionInvalid }
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) { return User{}, err }
	jit, err := m.settings.Bool(ctx, settings.OIDCJITProvisioningEnabled)
	if err != nil { return User{}, err }
	if !jit { return User{}, ErrOIDCLinkRequired }
	username = strings.TrimSpace(username)
	if len(username) < 2 { return User{}, errors.New("OIDC username claim is too short") }
	var existingID int64
	err = m.auth.db.QueryRowContext(ctx, "SELECT id FROM users WHERE username=? COLLATE NOCASE", username).Scan(&existingID)
	if err == nil {
		autoLink, err := m.settings.Bool(ctx, settings.OIDCAutoLinkEnabled)
		if err != nil { return User{}, err }
		if !autoLink { return User{}, ErrOIDCUsernameTaken }
		user, err := m.auth.UserByID(ctx, existingID)
		if err != nil || !user.Enabled { return User{}, ErrSessionInvalid }
		if _, err := m.linkIdentity(ctx, user.ID, provider.ID, provider.Issuer, subject); err != nil { return User{}, err }
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) { return User{}, err }
	tx, err := m.auth.db.BeginTx(ctx, nil)
	if err != nil { return User{}, err }
	defer tx.Rollback()
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,enabled,created_at) VALUES(?,?,1,?)", username, "!oidc", now)
	if err != nil { return User{}, err }
	id, err := result.LastInsertId(); if err != nil { return User{}, err }
	identityID, err := randomToken(12); if err != nil { return User{}, err }
	if _, err := tx.ExecContext(ctx, "INSERT INTO external_identities(id,provider_id,issuer,subject,user_id,created_at) VALUES(?,?,?,?,?,?)", identityID, provider.ID, provider.Issuer, subject, id, now); err != nil { return User{}, err }
	if err := tx.Commit(); err != nil { return User{}, err }
	return User{ID: id, Username: username, Enabled: true, CreatedAt: now}, nil
}

func (m *OIDCManager) ListIdentities(ctx context.Context, userID int64) ([]ExternalIdentity, error) {
	query, args := "SELECT id,provider_id,issuer,subject,user_id,created_at FROM external_identities", []any{}
	if userID > 0 { query += " WHERE user_id=?"; args = append(args, userID) }
	query += " ORDER BY created_at,id"
	rows, err := m.auth.db.QueryContext(ctx, query, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	out := make([]ExternalIdentity, 0)
	for rows.Next() {
		var item ExternalIdentity
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.Issuer, &item.Subject, &item.UserID, &item.CreatedAt); err != nil { return nil, err }
		out = append(out, item)
	}
	return out, rows.Err()
}

func (m *OIDCManager) LinkIdentity(ctx context.Context, userID int64, providerID, issuer, subject string) (ExternalIdentity, error) {
	provider, err := m.GetProvider(ctx, providerID)
	if err != nil { return ExternalIdentity{}, err }
	issuer = strings.TrimSpace(issuer)
	if issuer == "" { issuer = provider.Issuer }
	if issuer != provider.Issuer { return ExternalIdentity{}, errors.New("identity issuer must match provider issuer") }
	if _, err := m.auth.UserByID(ctx, userID); err != nil { return ExternalIdentity{}, err }
	return m.linkIdentity(ctx, userID, providerID, issuer, subject)
}

func (m *OIDCManager) linkIdentity(ctx context.Context, userID int64, providerID, issuer, subject string) (ExternalIdentity, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" { return ExternalIdentity{}, errors.New("subject is required") }
	id, err := randomToken(12); if err != nil { return ExternalIdentity{}, err }
	now := time.Now().Unix()
	if _, err := m.auth.db.ExecContext(ctx, "INSERT INTO external_identities(id,provider_id,issuer,subject,user_id,created_at) VALUES(?,?,?,?,?,?)", id, providerID, issuer, subject, userID, now); err != nil { return ExternalIdentity{}, err }
	return ExternalIdentity{ID: id, ProviderID: providerID, Issuer: issuer, Subject: subject, UserID: userID, CreatedAt: now}, nil
}

func (m *OIDCManager) UnlinkIdentity(ctx context.Context, id string) error {
	result, err := m.auth.db.ExecContext(ctx, "DELETE FROM external_identities WHERE id=?", strings.TrimSpace(id))
	if err != nil { return err }
	rows, err := result.RowsAffected(); if err != nil { return err }
	if rows != 1 { return sql.ErrNoRows }
	return nil
}

func boolInt(value bool) int { if value { return 1 }; return 0 }

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) { return false }
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy); sort.Strings(rightCopy)
	for i := range leftCopy { if leftCopy[i] != rightCopy[i] { return false } }
	return true
}