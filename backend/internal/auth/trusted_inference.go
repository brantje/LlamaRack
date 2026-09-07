package auth

import "context"

type trustedInferenceContextKey struct{}

// TrustedInferencePrincipal identifies the management principal that already
// authenticated before re-entering the inference gateway (Playground bridge).
type TrustedInferencePrincipal struct {
	Kind string
	ID   string
}

// WithTrustedInferenceContext marks an in-process request as already authenticated
// by the management API. The marker cannot be supplied by a remote HTTP client;
// it exists only in the Go request context used by the Playground bridge.
func WithTrustedInferenceContext(ctx context.Context, principal TrustedInferencePrincipal) context.Context {
	return context.WithValue(ctx, trustedInferenceContextKey{}, principal)
}

// TrustedInferencePrincipalFromContext returns the Playground management principal.
func TrustedInferencePrincipalFromContext(ctx context.Context) (TrustedInferencePrincipal, bool) {
	value, ok := ctx.Value(trustedInferenceContextKey{}).(TrustedInferencePrincipal)
	if !ok || value.Kind == "" || value.ID == "" {
		return TrustedInferencePrincipal{}, false
	}
	return value, true
}

func trustedInferenceContext(ctx context.Context) bool {
	_, ok := ctx.Value(trustedInferenceContextKey{}).(TrustedInferencePrincipal)
	return ok
}
