package lifecycle

import (
	"context"
	"testing"
)

func TestRequestCorrelationContext(t *testing.T) {
	t.Run("stores trimmed request id", func(t *testing.T) {
		ctx := WithRequestCorrelation(context.Background(), "  req-123  ")
		if got := RequestCorrelationFromContext(ctx); got != "req-123" {
			t.Fatalf("RequestCorrelationFromContext() = %q, want %q", got, "req-123")
		}
	})

	t.Run("empty id leaves context unchanged", func(t *testing.T) {
		ctx := context.Background()
		got := WithRequestCorrelation(ctx, "   ")
		if got != ctx {
			t.Fatal("WithRequestCorrelation() should return the original context for an empty id")
		}
		if value := RequestCorrelationFromContext(got); value != "" {
			t.Fatalf("RequestCorrelationFromContext() = %q, want empty", value)
		}
	})

	t.Run("nil context reads empty", func(t *testing.T) {
		if got := RequestCorrelationFromContext(nil); got != "" {
			t.Fatalf("RequestCorrelationFromContext(nil) = %q, want empty", got)
		}
	})

	t.Run("non string value is ignored", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), requestCorrelationContextKey{}, 42)
		if got := RequestCorrelationFromContext(ctx); got != "" {
			t.Fatalf("RequestCorrelationFromContext() = %q, want empty", got)
		}
	})
}
