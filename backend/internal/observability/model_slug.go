package observability

import (
	"context"
	"database/sql"
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
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET model_slug=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?)`, modelSlug, requestID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
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
	var identity RequestModelIdentity
	err := s.db.QueryRowContext(ctx, `SELECT r.instance_id,r.model_slug
		FROM inference_requests r JOIN inference_request_correlations c ON c.inference_request_id=r.id
		WHERE c.request_id=?`, requestID).Scan(&identity.InstanceID, &identity.ModelSlug)
	return identity, err
}
