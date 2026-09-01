package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayDiagnosticMessageAndSafePrefix(t *testing.T) {
	if got := safeAPIKeyPrefix("Bearer lcm_a91f4c_super_secret"); got != "lcm_a91f" {
		t.Fatalf("prefix=%q", got)
	}
	for _, value := range []string{"", "Basic abc", "Bearer", "Bearer one two"} {
		if got := safeAPIKeyPrefix(value); got != "" {
			t.Fatalf("unsafe header %q produced prefix %q", value, got)
		}
	}
	if got := gatewayDiagnosticMessage(http.MethodPost, "/v1/chat/completions", "qwen-coder-32b", true, 200, 1842*time.Millisecond, "lcm_a91f"); got != "POST /v1/chat/completions model=qwen-coder-32b stream=true 200 in 1.84s" {
		t.Fatalf("chat log=%q", got)
	}
	if got := gatewayDiagnosticMessage(http.MethodPost, "/v1/embeddings", "embeddings", false, 200, 41*time.Millisecond, "lcm_a91f"); got != "POST /v1/embeddings model=embeddings 200 in 41ms" {
		t.Fatalf("embedding log=%q", got)
	}
	if got := gatewayDiagnosticMessage(http.MethodGet, "/v1/models", "", false, 200, 3*time.Millisecond, "lcm_a91f"); got != "GET /v1/models 200 in 3ms key=lcm_a91f" {
		t.Fatalf("models log=%q", got)
	}
}

func TestDiagnosticResponseWriterPreservesFlushing(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &diagnosticResponseWriter{ResponseWriter: recorder}
	writer.Flush()
	if writer.StatusCode() != http.StatusOK || !recorder.Flushed {
		t.Fatalf("status=%d flushed=%v", writer.StatusCode(), recorder.Flushed)
	}
	writer.WriteHeader(http.StatusAccepted)
	if writer.StatusCode() != http.StatusOK {
		t.Fatalf("first status should remain recorded, got %d", writer.StatusCode())
	}
}
