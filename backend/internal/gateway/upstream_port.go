package gateway

import (
	"net/http"

	"github.com/brantje/llamarack/backend/internal/lifecycle"
)

type upstreamPortResponseWriter struct {
	http.ResponseWriter
	resolved bool
}

func (w *upstreamPortResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *upstreamPortResponseWriter) resolve() {
	if w.resolved {
		return
	}
	w.resolved = true
	// The gateway writes X-Llamarack-Upstream-Port from the already-resolved
	// worker target before proxying. Do not resolve X-Llamarack-Instance here:
	// that header is the public mutable slug, while lifecycle ownership is keyed
	// by the durable Instance UUID. Looking it up here would also add an extra
	// storage/runtime lookup to the hot /v1 response path.
}

func (w *upstreamPortResponseWriter) WriteHeader(status int) {
	w.resolve()
	w.ResponseWriter.WriteHeader(status)
}

func (w *upstreamPortResponseWriter) Write(body []byte) (int, error) {
	w.resolve()
	return w.ResponseWriter.Write(body)
}

func (w *upstreamPortResponseWriter) Flush() {
	w.resolve()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// WithUpstreamPortHeader preserves the response-writer capabilities used by
// proxied responses. The gateway itself attaches the resolved internal worker
// port from the already-selected target, so this wrapper never needs to look up
// an Instance by response metadata.
func WithUpstreamPortHeader(service *lifecycle.Service, next http.Handler) http.Handler {
	if service == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&upstreamPortResponseWriter{ResponseWriter: w}, r)
	})
}
