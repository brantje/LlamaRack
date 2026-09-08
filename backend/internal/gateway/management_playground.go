package gateway

import (
	"context"
	"encoding/json"
	"net/http"
)

const managementPlaygroundBearer = "Bearer management-playground-internal"

type managementPlaygroundContextKey struct{}

func withManagementPlaygroundContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, managementPlaygroundContextKey{}, true)
}

func isManagementPlaygroundRequest(ctx context.Context) bool {
	value, _ := ctx.Value(managementPlaygroundContextKey{}).(bool)
	return value
}

func ensurePlaygroundTimings(body []byte) []byte {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil {
		return body
	}
	if _, exists := object["timings_per_token"]; exists {
		return body
	}
	object["timings_per_token"] = json.RawMessage("true")
	updated, err := json.Marshal(object)
	if err != nil {
		return body
	}
	return updated
}

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

		request := r.Clone(withManagementPlaygroundContext(r.Context()))
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
