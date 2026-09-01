package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestSetProductHeaderWritesCanonicalName(t *testing.T) {
	header := http.Header{}
	setProductHeader(header, headerRequestID, "lr_abc")
	if got := header.Get(headerRequestID); got != "lr_abc" {
		t.Fatalf("canonical = %q", got)
	}
	if got := header.Get("X-LlamaCPP-Manager-Request-ID"); got != "" {
		t.Fatalf("previous product header = %q", got)
	}
}

func TestGetProductHeaderReadsCanonicalName(t *testing.T) {
	header := http.Header{}
	if got := getProductHeader(header, headerInstance); got != "" {
		t.Fatalf("empty header = %q", got)
	}
	header.Set("X-LlamaCPP-Manager-Instance", "previous-instance")
	if got := getProductHeader(header, headerInstance); got != "" {
		t.Fatalf("previous product header = %q", got)
	}
	header.Set(headerInstance, "canonical-instance")
	if got := getProductHeader(header, headerInstance); got != "canonical-instance" {
		t.Fatalf("canonical read = %q", got)
	}
}

func TestCORSExposeHeadersIncludesCanonicalProductNames(t *testing.T) {
	exposed := CORSExposeHeaders()
	for _, name := range []string{
		headerRequestID, headerInstance, headerUpstreamPort,
		"X-LiteLLM-Trace-ID", "X-LiteLLM-Session-ID",
	} {
		if !strings.Contains(exposed, name) {
			t.Fatalf("missing %s in %q", name, exposed)
		}
	}
	if strings.Contains(exposed, "X-LlamaCPP-Manager-") {
		t.Fatalf("previous product headers still exposed: %q", exposed)
	}
}
