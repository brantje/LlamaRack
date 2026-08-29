package auth

import (
	"context"
	"errors"
	"strings"
)

// TestProviderInput validates and probes an OIDC provider configuration without
// persisting it. It intentionally mirrors TestProvider's discovery/JWKS checks
// so administrators can verify a new provider before saving it.
func (m *OIDCManager) TestProviderInput(ctx context.Context, in OIDCProviderInput) error {
	in, err := validateProviderInput(in)
	if err != nil {
		return err
	}
	if in.ClientSecret == nil || strings.TrimSpace(*in.ClientSecret) == "" {
		return errors.New("client_secret is required")
	}

	provider := OIDCProvider{
		Name:                  in.Name,
		Enabled:               in.Enabled,
		Issuer:                in.Issuer,
		DiscoveryURL:          in.DiscoveryURL,
		ClientID:              in.ClientID,
		Scopes:                in.Scopes,
		UsernameClaim:         in.UsernameClaim,
		AuthorizationEndpoint: in.AuthorizationEndpoint,
		TokenEndpoint:         in.TokenEndpoint,
		JWKSURL:               in.JWKSURL,
		SecretConfigured:      true,
	}
	resolved, err := m.resolveProvider(ctx, provider)
	if err != nil {
		return err
	}
	return m.probeJWKS(ctx, resolved.JWKSURL)
}
