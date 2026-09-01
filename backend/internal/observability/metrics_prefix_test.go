package observability

import (
	"strings"
	"testing"
)

func TestWriteMetricHelpersEmitCanonicalNames(t *testing.T) {
	var builder strings.Builder
	writeMetricHelp(&builder, "gateway_requests_total", "OpenAI-compatible gateway requests.")
	writeMetricType(&builder, "gateway_requests_total", "counter")
	writeMetricSample(&builder, "gateway_requests_total", `{instance_id="one"}`, "3")
	got := builder.String()
	for _, want := range []string{
		"# HELP llamarack_gateway_requests_total OpenAI-compatible gateway requests.",
		"# TYPE llamarack_gateway_requests_total counter",
		`llamarack_gateway_requests_total{instance_id="one"} 3`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "llamacpp_manager_") {
		t.Fatalf("previous product metrics still emitted: %q", got)
	}
}
