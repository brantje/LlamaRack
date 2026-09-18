package observability

import (
	"context"
	"fmt"
	"strings"
)

// SetRequestModelSlug records the exact OpenAI model slug supplied for a request.
// It is historical context and is never rewritten when an Instance is renamed.
func (s *Service) SetRequestModelSlug(ctx context.Context, requestID, modelSlug string) error {
	requestID = strings.TrimSpace(requestID)
	modelSlug = strings.TrimSpace(modelSlug)
	if requestID == "" || modelSlug == "" {
		return nil
	}
	if handled, err := s.bufferModelSlug(requestID, modelSlug); handled {
		return err
	}
	if s.writebackEnabled() {
		return nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	return s.store.SetRequestModelSlug(ctx, requestID, modelSlug)
}

type RequestModelIdentity struct {
	InstanceID string `json:"instance_id,omitempty"`
	ModelSlug  string `json:"model_slug,omitempty"`
}

func (s *Service) RequestModelIdentity(ctx context.Context, requestID string) (RequestModelIdentity, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return RequestModelIdentity{}, fmt.Errorf("request_id is required")
	}
	if identity, ok := s.bufferedRequestModelIdentity(requestID); ok {
		return identity, nil
	}
	return s.store.RequestModelIdentity(ctx, requestID)
}
