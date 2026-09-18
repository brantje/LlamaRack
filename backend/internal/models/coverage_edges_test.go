package models

import (
	"context"
	"path/filepath"
	"testing"
)

func TestServiceDBAndDiscoveryEdgePaths(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	db := testModelDB(t, s)
	if got := normalizePriority(" low "); got != "low" {
		t.Fatalf("normalizePriority low=%q", got)
	}
	if got := normalizePriority("unexpected"); got != "normal" {
		t.Fatalf("normalizePriority default=%q", got)
	}

	missingRoot := New(db, filepath.Join(dir, "does-not-exist"))
	if _, err := missingRoot.AvailableGGUFs(ctx); err == nil {
		t.Fatal("expected discovery error for missing model directory")
	}
}

func TestAvailableGGUFsReturnsDatabaseQueryError(t *testing.T) {
	s, _ := testModelService(t)
	db := testModelDB(t, s)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AvailableGGUFs(context.Background()); err == nil {
		t.Fatal("expected discovery query error after database close")
	}
}
