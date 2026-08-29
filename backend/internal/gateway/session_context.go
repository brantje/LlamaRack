package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/observability"
)

const (
	headerSessionID            = "X-LiteLLM-Session-ID"
	preAuthRequestBodyBytes    = 64 << 10
)

var genericSessionHeader = regexp.MustCompile(`(?i)^x-.+-session-id$`)

type sessionEnvelope struct {
	SessionID string `json:"session_id"`
	Metadata  struct {
		SessionID string `json:"session_id"`
	} `json:"metadata"`
	LiteLLMMetadata struct {
		SessionID string `json:"session_id"`
	} `json:"litellm_metadata"`
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

func shouldPreAuthenticateRequestBody(r *http.Request) bool {
	return r.Body != nil && (r.ContentLength < 0 || r.ContentLength > preAuthRequestBodyBytes)
}

// WithRequestLogContext mirrors LiteLLM's session grouping inputs without
// changing the OpenAI-compatible payload. Potentially large or chunked request
// bodies are authenticated before the gateway can buffer them. Invalid callers
// keep a small metadata budget so the normal gateway path can still create and
// finalize the failed request log without allocating the full request body.
func WithRequestLogContext(next http.Handler, service *observability.Service) http.Handler {
	if service == nil {
		return next
	}
	gateway, _ := next.(*Gateway)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gateway != nil && shouldPreAuthenticateRequestBody(r) {
			if _, err := gateway.authenticateKey(r.Context(), r.Header.Get("Authorization")); err != nil {
				r.Body = http.MaxBytesReader(w, r.Body, preAuthRequestBodyBytes)
			}
		}

		sessionFromHeader := sessionIDFromHeaders(r)
		bodySessionID := make(chan string, 1)
		if r.Body == nil {
			bodySessionID <- ""
		} else {
			reader, writer := io.Pipe()
			r.Body = &sessionCaptureBody{source: r.Body, pipe: writer}
			go func() {
				defer reader.Close()
				var envelope sessionEnvelope
				decoder := json.NewDecoder(reader)
				if err := decoder.Decode(&envelope); err != nil {
					bodySessionID <- ""
					_, _ = io.Copy(io.Discard, reader)
					return
				}
				bodySessionID <- sessionIDFromEnvelope(envelope)
				_, _ = io.Copy(io.Discard, reader)
			}()
		}

		if sessionFromHeader != "" {
			w.Header().Set(headerSessionID, sessionFromHeader)
			r.Header.Del(headerSessionID)
		}
		next.ServeHTTP(w, r)

		sessionID := sessionFromHeader
		if sessionID == "" {
			select {
			case sessionID = <-bodySessionID:
			case <-time.After(100 * time.Millisecond):
			}
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
