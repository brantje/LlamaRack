package gateway

import (
	"net/http"
)

const managementPlaygroundBearer = "Bearer management-playground-internal"

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

		request := r.Clone(r.Context())
		urlCopy := *r.URL
		urlCopy.Path = "/v1/chat/completions"
		urlCopy.RawPath = ""
		request.URL = &urlCopy
		request.Header = r.Header.Clone()
		// Never pass the management bearer token into inference authentication.
		// Replace it with a non-secret sentinel so the gateway keeps its normal
		// Bearer-shape validation; the private context marker makes the auth
		// service skip API-key lookup. The gateway strips Authorization before
		// proxying to llama-server, so the sentinel never reaches the worker.
		request.Header.Set("Authorization", managementPlaygroundBearer)

		next.ServeHTTP(w, request)
	})
}
