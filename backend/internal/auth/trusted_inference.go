package auth

import "context"

type trustedInferenceContextKey struct{}

// WithTrustedInferenceContext marks an in-process request as already authenticated
// by the management API. The marker cannot be supplied by a remote HTTP client;
// it exists only in the Go request context used by the Playground bridge.
func WithTrustedInferenceContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, trustedInferenceContextKey{}, struct{}{})
}

func trustedInferenceContext(ctx context.Context) bool {
	_, ok := ctx.Value(trustedInferenceContextKey{}).(struct{})
	return ok
}
