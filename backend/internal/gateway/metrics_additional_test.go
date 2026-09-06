package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCalculateResponseMetricsDerivesTTFTAndGenerationRate(t *testing.T) {
	started := time.Unix(100, 0)
	firstByte := started.Add(200 * time.Millisecond)
	finished := firstByte.Add(2 * time.Second)

	metrics := calculateResponseMetrics(started, firstByte, finished, usageValues{
		prompt:    4,
		generated: 10,
		total:     14,
	})

	if metrics.ttftMS == nil || math.Abs(*metrics.ttftMS-200) > 0.001 {
		t.Fatalf("ttft=%v", metrics.ttftMS)
	}
	if metrics.generationTPS == nil || math.Abs(*metrics.generationTPS-5) > 0.001 {
		t.Fatalf("generation tps=%v", metrics.generationTPS)
	}
	if metrics.promptTokens != 4 || metrics.generatedTokens != 10 || metrics.totalTokens != 14 {
		t.Fatalf("tokens=%+v", metrics)
	}
}

func TestCalculateResponseMetricsUsesWholeDurationWithoutFirstByte(t *testing.T) {
	started := time.Unix(200, 0)
	finished := started.Add(4 * time.Second)

	metrics := calculateResponseMetrics(started, time.Time{}, finished, usageValues{generated: 8})

	if metrics.ttftMS != nil {
		t.Fatalf("unexpected ttft=%v", *metrics.ttftMS)
	}
	if metrics.generationTPS == nil || math.Abs(*metrics.generationTPS-2) > 0.001 {
		t.Fatalf("generation tps=%v", metrics.generationTPS)
	}
}

func TestCalculateResponseMetricsPreservesProvidedRatesAndSkipsNonPositiveWindow(t *testing.T) {
	promptTPS := 12.5
	generationTPS := 7.25
	started := time.Unix(300, 0)

	metrics := calculateResponseMetrics(started, time.Time{}, started, usageValues{
		generated:     5,
		promptTPS:     &promptTPS,
		generationTPS: &generationTPS,
	})
	if metrics.promptTPS == nil || *metrics.promptTPS != promptTPS {
		t.Fatalf("prompt tps=%v", metrics.promptTPS)
	}
	if metrics.generationTPS == nil || *metrics.generationTPS != generationTPS {
		t.Fatalf("generation tps=%v", metrics.generationTPS)
	}

	metrics = calculateResponseMetrics(started, time.Time{}, started, usageValues{generated: 5})
	if metrics.generationTPS != nil {
		t.Fatalf("expected no derived rate, got %v", *metrics.generationTPS)
	}
}

func TestNumberValueHandlesJSONNumbersAndUnsupportedValues(t *testing.T) {
	value, ok := numberValue(json.Number("12.75"))
	if !ok || value != 12.75 {
		t.Fatalf("json number=%v ok=%v", value, ok)
	}
	if _, ok := numberValue(json.Number("not-a-number")); ok {
		t.Fatal("invalid json.Number unexpectedly accepted")
	}
	if _, ok := numberValue("12.75"); ok {
		t.Fatal("string unexpectedly accepted")
	}
}

func TestResponseObserverDefaultsToOKBeforeWrite(t *testing.T) {
	recorder := httptest.NewRecorder()
	observer := newResponseObserver(recorder, false)
	if got := observer.StatusCode(); got != http.StatusOK {
		t.Fatalf("status=%d", got)
	}
}
