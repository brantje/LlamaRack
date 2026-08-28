package settings

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

const DiscoverHybridRecommendations = "discover_hybrid_recommendations"

type Discover struct {
	HybridRecommendations Value `json:"hybrid_recommendations_enabled"`
}

func (s *Service) Discover(ctx context.Context) (Discover, error) {
	var stored string
	err := s.db.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", DiscoverHybridRecommendations).Scan(&stored)
	if err == nil {
		value, parseErr := strconv.ParseBool(stored)
		if parseErr != nil {
			return Discover{}, parseErr
		}
		return Discover{HybridRecommendations: Value{Value: value, Source: "database", Editable: true}}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Discover{}, err
	}
	return Discover{HybridRecommendations: Value{Value: true, Source: "default", Editable: true}}, nil
}

func (s *Service) SetDiscoverHybridRecommendations(ctx context.Context, enabled bool) (Discover, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)
		ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=excluded.updated_at`,
		DiscoverHybridRecommendations, strconv.FormatBool(enabled), time.Now().Unix())
	if err != nil {
		return Discover{}, err
	}
	return Discover{HybridRecommendations: Value{Value: enabled, Source: "database", Editable: true}}, nil
}
