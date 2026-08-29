package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

const headerSessionID = "X-LiteLLM-Session-ID"

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

func resolveSessionID(r *http.Request, body []byte) string {
	if value := normalizeSessionID(r.Header.Get(headerSessionID)); value != "" {
		return value
	}
	var envelope sessionEnvelope
	if len(bytes.TrimSpace(body)) > 0 && json.Unmarshal(body, &envelope) == nil {
		for _, value := range []string{envelope.LiteLLMMetadata.SessionID, envelope.Metadata.SessionID, envelope.SessionID} {
			if normalized := normalizeSessionID(value); normalized != "" {
				return normalized
			}
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

func (g *Gateway) updateRequestLogContext(r *http.Request, requestID, sessionID, instanceID string) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(r.Context())
	defer cancel()
	if err := g.observability.UpdateRequestLogContext(persistCtx, requestID, sessionID, instanceID); err != nil {
		slog.Warn("update inference request log context failed", "request_id", requestID, "session_id", sessionID, "instance_id", instanceID, "error", err)
	}
}
