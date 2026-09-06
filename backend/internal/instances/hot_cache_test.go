package instances

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestHotCacheIsOptInAndReturnsClones(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	item, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Cache Me", GPUDevices: []string{"CUDA0"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE instances SET name='SQL visible' WHERE id=?", item.ID); err != nil {
		t.Fatal(err)
	}
	uncached, err := s.GetByID(ctx, item.ID)
	if err != nil || uncached.Name != "SQL visible" {
		t.Fatalf("cache should be off by default: %+v err=%v", uncached, err)
	}

	s.EnableHotCache()
	warm, err := s.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	warm.GPUDevices[0] = "MUTATED"
	again, err := s.GetByID(ctx, item.ID)
	if err != nil || again.GPUDevices[0] != "CUDA0" {
		t.Fatalf("cached instance aliased caller memory: %+v err=%v", again, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE instances SET name='raw change' WHERE id=?", item.ID); err != nil {
		t.Fatal(err)
	}
	cached, err := s.GetByID(ctx, item.ID)
	if err != nil || cached.Name != "SQL visible" {
		t.Fatalf("hot cache unexpectedly observed raw SQL: %+v err=%v", cached, err)
	}
}

func TestHotCacheHitsByIDAndSlugWithoutSQLite(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	item, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Hot Path", Slug: "hot-path"})
	if err != nil {
		t.Fatal(err)
	}
	s.EnableHotCache()
	if _, err := s.GetBySlug(ctx, item.Slug); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	blockedCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if got, err := s.GetByID(blockedCtx, item.ID); err != nil || got.ID != item.ID {
		t.Fatalf("id cache hit touched SQLite: %+v err=%v", got, err)
	}
	if got, err := s.GetBySlug(blockedCtx, item.Slug); err != nil || got.ID != item.ID {
		t.Fatalf("slug cache hit touched SQLite: %+v err=%v", got, err)
	}
}

func TestHotCacheInvalidatesCreateUpdateDeleteAndOldSlug(t *testing.T) {
	ctx := context.Background()
	s, _ := testService(t)
	s.EnableHotCache()
	item, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Original", Slug: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.cachedBySlug("original"); !ok || got.ID != item.ID {
		t.Fatalf("create did not populate cache: %+v ok=%v", got, ok)
	}
	enabled, autoload, eviction := item.Enabled, item.Autoload, item.EvictionEnabled
	updated, err := s.Update(ctx, item.ID, UpdateInput{
		Name: "Updated", Slug: "updated", Enabled: &enabled, Autoload: &autoload, AlwaysOn: item.AlwaysOn,
		Priority: item.Priority, EvictionEnabled: &eviction, IdleUnloadSeconds: item.IdleUnloadSeconds,
		MaxPendingRequests: &item.MaxPendingRequests, GPUMode: item.GPUMode, GPUDevices: item.GPUDevices,
		TensorSplit: item.TensorSplit, RequestLogMode: item.RequestLogMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != item.ID || updated.Slug != "updated" {
		t.Fatalf("updated=%+v", updated)
	}
	if _, ok := s.cachedBySlug("original"); ok {
		t.Fatal("old slug remained cached")
	}
	if _, err := s.GetBySlug(ctx, "original"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old slug lookup=%v", err)
	}
	if err := s.Delete(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.cachedByID(item.ID); ok {
		t.Fatal("deleted instance remained cached")
	}
}

func TestHotCacheGenerationRejectsReadAfterUpdateAndDelete(t *testing.T) {
	s, _ := testService(t)
	s.EnableHotCache()
	stale := Instance{ID: "instance-a", Slug: "old", Name: "Old"}

	_, updateGeneration, _ := s.cachedByIDAtGeneration(stale.ID)
	updated := stale
	updated.Slug = "new"
	updated.Name = "New"
	s.rememberHot(updated)
	if s.rememberHotIfGeneration(stale, updateGeneration) {
		t.Fatal("stale read repopulated cache after update")
	}
	if got, ok := s.cachedByID(stale.ID); !ok || got.Name != "New" || got.Slug != "new" {
		t.Fatalf("updated cache=%+v ok=%v", got, ok)
	}

	_, deleteGeneration, _ := s.cachedByIDAtGeneration(stale.ID)
	s.forgetHot(stale.ID)
	if s.rememberHotIfGeneration(stale, deleteGeneration) {
		t.Fatal("stale read repopulated cache after delete")
	}
	if _, ok := s.cachedByID(stale.ID); ok {
		t.Fatal("deleted instance returned to cache")
	}
}
