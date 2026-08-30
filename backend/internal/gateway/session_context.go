package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/observability"
	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

const headerSessionID = "X-LiteLLM-Session-ID"

var genericSessionHeader = regexp.MustCompile(`(?i)^x-.+-session-id$`)

type sessionEnvelope struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	SessionID string `json:"session_id"`
	Metadata  struct {
		SessionID string `json:"session_id"`
	} `json:"metadata"`
	LiteLLMMetadata struct {
		SessionID string `json:"session_id"`
	} `json:"litellm_metadata"`
}

type requestLogMetadata struct {
	sessionID string
	model     string
	stream    bool
}

type sessionCaptureBody struct {
	source io.ReadCloser
	pipe   *io.PipeWriter
}

func (b *sessionCaptureBody) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	if n > 0 {
		_, _ = b.pipe.Write(p[:n])
	}
	if err != nil {
		_ = b.pipe.CloseWithError(nil)
	}
	return n, err
}

func (b *sessionCaptureBody) Close() error {
	_ = b.pipe.Close()
	return b.source.Close()
}

type diagnosticResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *diagnosticResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *diagnosticResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *diagnosticResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}
func (w *diagnosticResponseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (w *diagnosticResponseWriter) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// WithRequestLogContext mirrors LiteLLM's session grouping inputs without
// changing the OpenAI-compatible payload. Session identity is consumed here so
// the gateway's trace resolver cannot accidentally treat X-LiteLLM-Session-ID
// as a trace ID; LiteLLM keeps those identities distinct.
func WithRequestLogContext(next http.Handler, service *observability.Service) http.Handler {
	if service == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		traceID, ok := suppliedTraceID(r, "")
		if !ok {
			traceID = newTraceID()
			r.Header.Set(headerTraceID, traceID)
		}
		r = r.WithContext(lifecycle.WithRequestCorrelation(r.Context(), traceID))
		sessionFromHeader := sessionIDFromHeaders(r)
		bodyMetadata := make(chan requestLogMetadata, 1)
		if r.Body == nil {
			bodyMetadata <- requestLogMetadata{}
		} else {
			reader, writer := io.Pipe()
			r.Body = &sessionCaptureBody{source: r.Body, pipe: writer}
			go func() {
				defer reader.Close()
				var envelope sessionEnvelope
				decoder := json.NewDecoder(reader)
				if err := decoder.Decode(&envelope); err != nil {
					bodyMetadata <- requestLogMetadata{}
					_, _ = io.Copy(io.Discard, reader)
					return
				}
				bodyMetadata <- requestLogMetadata{
					sessionID: sessionIDFromEnvelope(envelope),
					model:     strings.TrimSpace(envelope.Model),
					stream:    envelope.Stream,
				}
				_, _ = io.Copy(io.Discard, reader)
			}()
		}

		if sessionFromHeader != "" {
			w.Header().Set(headerSessionID, sessionFromHeader)
			r.Header.Del(headerSessionID)
		}
		keyPrefix := safeAPIKeyPrefix(r.Header.Get("Authorization"))
		observed := &diagnosticResponseWriter{ResponseWriter: w}
		next.ServeHTTP(observed, r)

		metadata := requestLogMetadata{}
		select {
		case metadata = <-bodyMetadata:
		case <-time.After(100 * time.Millisecond):
		}
		systemlog.Log(systemlog.Info, "gateway", gatewayDiagnosticMessage(r.Method, r.URL.Path, metadata.model, metadata.stream, observed.StatusCode(), time.Since(started), keyPrefix))

		sessionID := sessionFromHeader
		if sessionID == "" {
			sessionID = metadata.sessionID
		}
		if sessionID == "" {
			sessionID = newTraceID()
		}
		requestID := strings.TrimSpace(w.Header().Get(headerRequestID))
		if requestID == "" {
			return
		}
		instanceID := strings.TrimSpace(w.Header().Get(headerInstance))
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if err := service.UpdateRequestLogContext(persistCtx, requestID, sessionID, instanceID); err != nil {
			slog.Warn("update inference request log context failed", "request_id", requestID, "session_id", sessionID, "instance_id", instanceID, "error", err)
		}
	})
}

func safeAPIKeyPrefix(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	prefix := strings.TrimSpace(parts[1])
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return prefix
}

func gatewayDiagnosticMessage(method, path, model string, stream bool, status int, duration time.Duration, keyPrefix string) string {
	parts := []string{strings.ToUpper(strings.TrimSpace(method)), path}
	if model != "" {
		parts = append(parts, "model="+model)
	}
	if stream {
		parts = append(parts, "stream=true")
	}
	parts = append(parts, fmt.Sprintf("%d", status), "in", systemlog.FormatDuration(duration))
	if keyPrefix != "" && strings.EqualFold(method, http.MethodGet) {
		parts = append(parts, "key="+keyPrefix)
	}
	return strings.Join(parts, " ")
}

func sessionIDFromEnvelope(envelope sessionEnvelope) string {
	for _, value := range []string{envelope.LiteLLMMetadata.SessionID, envelope.Metadata.SessionID, envelope.SessionID} {
		if normalized := normalizeSessionID(value); normalized != "" {
			return normalized
		}
	}
	return ""
}

func sessionIDFromHeaders(r *http.Request) string {
	if value := normalizeSessionID(r.Header.Get(headerSessionID)); value != "" {
		return value
	}
	for key, values := range r.Header {
		if strings.EqualFold(key, "X-LiteLLM-Trace-ID") || strings.EqualFold(key, headerSessionID) || !genericSessionHeader.MatchString(key) {
			continue
		}
		for _, value := range values {
			if normalized := normalizeSessionID(value); normalized != "" {
				return normalized
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.UserAgent())), "codex") {
		for _, key := range []string{"Session-ID", "Session_Id", "Thread-ID", "Conversation_ID"} {
			if value := normalizeSessionID(r.Header.Get(key)); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeSessionID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 {
		return ""
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return value
}
