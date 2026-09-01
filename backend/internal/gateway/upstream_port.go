package gateway

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/lifecycle"
)

type upstreamPortResponseWriter struct {
	http.ResponseWriter
	request   *http.Request
	lifecycle *lifecycle.Service
	resolved  bool
}

func (w *upstreamPortResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *upstreamPortResponseWriter) resolve() {
	if w.resolved {
		return
	}
	w.resolved = true
	instanceID := strings.TrimSpace(getProductHeader(w.Header(), headerInstance))
	if instanceID == "" || w.lifecycle == nil {
		return
	}
	runtime, err := w.lifecycle.RuntimeInstance(w.request.Context(), instanceID)
	if err != nil || runtime.Port <= 0 {
		return
	}
	setProductHeader(w.Header(), headerUpstreamPort, strconv.Itoa(runtime.Port))
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

// WithUpstreamPortHeader exposes the resolved internal worker port as response
// metadata while requests continue to flow exclusively through the public
// manager gateway. It never exposes a direct worker URL or credentials.
func WithUpstreamPortHeader(service *lifecycle.Service, next http.Handler) http.Handler {
	if service == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&upstreamPortResponseWriter{ResponseWriter: w, request: r, lifecycle: service}, r)
	})
}
