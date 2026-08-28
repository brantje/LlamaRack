package settings

import (
	"context"
	"testing"
)

func TestDiscoverHybridRecommendationsDefaultAndPersistence(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)

	value, err := s.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.HybridRecommendations.Value != true || value.HybridRecommendations.Source != "default" || !value.HybridRecommendations.Editable {
		t.Fatalf("default=%+v", value.HybridRecommendations)
	}

	value, err = s.SetDiscoverHybridRecommendations(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if value.HybridRecommendations.Value != false || value.HybridRecommendations.Source != "database" {
		t.Fatalf("saved=%+v", value.HybridRecommendations)
	}

	value, err = s.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value.HybridRecommendations.Value != false || value.HybridRecommendations.Source != "database" || !value.HybridRecommendations.Editable {
		t.Fatalf("persisted=%+v", value.HybridRecommendations)
	}
}
