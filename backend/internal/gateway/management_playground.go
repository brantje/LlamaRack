package gateway

import (
	"net/http"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
)

// NewManagementPlaygroundProxy re-enters the normal OpenAI-compatible gateway
// after the management API has authenticated the operator. It deliberately
// rewrites to the public inference route so lifecycle, autoload, eviction,
// observability and request logging all use the same gateway implementation.
func NewManagementPlaygroundProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		request := r.Clone(auth.WithTrustedInferenceContext(r.Context()))
		urlCopy := *r.URL
		urlCopy.Path = "/v1/chat/completions"
		urlCopy.RawPath = ""
		request.URL = &urlCopy
		request.Header = r.Header.Clone()
		// Never pass the management bearer token into inference authentication or
		// onward to llama-server. The private context marker above is the trust
		// boundary for this in-process bridge.
		request.Header.Del("Authorization")

		next.ServeHTTP(w, request)
	})
}
