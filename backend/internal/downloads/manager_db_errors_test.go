package downloads

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/huggingface"
)

func TestDatabaseFailuresAreReturned(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	if err := manager.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	detail, selected := artifact("acme/demo", "rev", "closed-db", huggingface.File{Path: "demo.gguf", Size: 1})
	if _, err := manager.CreateHuggingFace(ctx, detail, selected); err == nil {
		t.Fatal("CreateHuggingFace should return a closed-database error")
	}
	if _, err := manager.List(ctx); err == nil {
		t.Fatal("List should return a closed-database error")
	}
	if _, err := manager.Get(ctx, "missing"); err == nil {
		t.Fatal("Get should return a closed-database error")
	}
	if err := manager.ResumePending(ctx); err == nil {
		t.Fatal("ResumePending should return a closed-database error")
	}
	if _, err := manager.Retry(ctx, "missing"); err == nil {
		t.Fatal("Retry should return a closed-database error")
	}
	if err := manager.Cancel(ctx, "missing"); err == nil {
		t.Fatal("Cancel should return a closed-database error")
	}
	if err := manager.refreshAggregate(ctx, "missing", 0); err == nil {
		t.Fatal("refreshAggregate should return a closed-database error")
	}
	if _, err := manager.files(ctx, "missing"); err == nil {
		t.Fatal("files should return a closed-database error")
	}
	if err := manager.run(ctx, "missing"); err == nil {
		t.Fatal("run should return a closed-database error")
	}
}

func TestCancelledContextStopsRunBeforeTransfer(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	insertJob(t, manager, "cancelled-context", StateQueued, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.run(ctx, "cancelled-context"); err == nil {
		t.Fatal("expected cancelled context")
	}
}

func TestRemoteIdentityHeaderFallbacksAndRequestValidation(t *testing.T) {
	manager, server, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Linked-Etag", " linked-etag ")
		w.Header().Set("X-Linked-Size", "not-a-number")
		w.Header().Set("Content-Length", "9")
		w.WriteHeader(http.StatusOK)
	}))

	etag, size, err := manager.remoteIdentity(context.Background(), server.URL+"/model.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if etag != "linked-etag" || size != 9 {
		t.Fatalf("identity = etag %q size %d", etag, size)
	}

	if _, _, err := manager.remoteIdentity(context.Background(), "https://example.com/model.gguf"); err == nil || !strings.Contains(err.Error(), "non-Hugging Face") {
		t.Fatalf("foreign HEAD error = %v", err)
	}
	if _, err := manager.get(context.Background(), "https://example.com/model.gguf", 5); err == nil || !strings.Contains(err.Error(), "non-Hugging Face") {
		t.Fatalf("foreign GET error = %v", err)
	}
}
