package settings

import (
	"context"
	"errors"

	"github.com/brantje/llamarack/backend/internal/database"
	"strconv"
	"time"
)

const DiscoverHybridRecommendations = "discover_hybrid_recommendations"

type Discover struct {
	HybridRecommendations Value `json:"hybrid_recommendations_enabled"`
}

func (s *Service) Discover(ctx context.Context) (Discover, error) {
	stored, err := s.store.Get(ctx, DiscoverHybridRecommendations)
	if err == nil {
		value, parseErr := strconv.ParseBool(stored)
		if parseErr != nil {
			return Discover{}, parseErr
		}
		return Discover{HybridRecommendations: Value{Value: value, Source: "database", Editable: true}}, nil
	}
	if !errors.Is(err, database.ErrNotFound) {
		return Discover{}, err
	}
	return Discover{HybridRecommendations: Value{Value: true, Source: "default", Editable: true}}, nil
}

func (s *Service) SetDiscoverHybridRecommendations(ctx context.Context, enabled bool) (Discover, error) {
	err := s.store.Set(ctx, DiscoverHybridRecommendations, strconv.FormatBool(enabled), time.Now().Unix())
	if err != nil {
		return Discover{}, err
	}
	return Discover{HybridRecommendations: Value{Value: enabled, Source: "database", Editable: true}}, nil
}
