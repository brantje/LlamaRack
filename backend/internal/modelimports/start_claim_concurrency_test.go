package modelimports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
	_ "github.com/jackc/pgx/v5/stdlib"
)


type claimErrorStore struct {
	result sql.Result
	err    error
}

func (s claimErrorStore) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return s.result, s.err
}

func (claimErrorStore) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("unexpected QueryContext")
}

func (claimErrorStore) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected QueryRowContext")
}

func (claimErrorStore) Close() error { return nil }

type rowsAffectedErrorResult struct{}

func (rowsAffectedErrorResult) LastInsertId() (int64, error) { return 0, nil }

func (rowsAffectedErrorResult) RowsAffected() (int64, error) {
	return 0, errors.New("rows affected unavailable")
}

func TestStartClaimStoreErrorPaths(t *testing.T) {
	ctx := context.Background()
	claimed, err := NewStore(claimErrorStore{err: errors.New("exec failed")}).ClaimStartAttempt(ctx, "import")
	if err == nil || claimed {
		t.Fatalf("exec failure claimed=%v err=%v", claimed, err)
	}

	claimed, err = NewStore(claimErrorStore{result: rowsAffectedErrorResult{}}).ClaimStartAttempt(ctx, "import")
	if err == nil || claimed {
		t.Fatalf("rows-affected failure claimed=%v err=%v", claimed, err)
	}

	claimed, err = NewStore(claimErrorStore{}).CompletePrepared(ctx, "import", "instance")
	if err == nil || claimed {
		t.Fatalf("begin failure claimed=%v err=%v", claimed, err)
	}
}

type preparedBarrierGate struct {
	mu        sync.Mutex
	remaining int
	release   chan struct{}
}

func newPreparedBarrierGate(workers int) *preparedBarrierGate {
	return &preparedBarrierGate{remaining: workers, release: make(chan struct{})}
}

func (g *preparedBarrierGate) arrive() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.remaining--
	if g.remaining == 0 {
		close(g.release)
	}
}

type preparedBarrierStore struct {
	Store
	gate *preparedBarrierGate
}

func (s *preparedBarrierStore) Prepared(ctx context.Context) ([]pendingImport, error) {
	items, err := s.Store.Prepared(ctx)
	if err != nil {
		return nil, err
	}
	s.gate.arrive()
	select {
	case <-s.gate.release:
		return items, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func seedReadyAutoStartImport(t *testing.T, ctx context.Context, modelsDir string, db database.Store, modelService *models.Service, instanceService *instances.Service, prefix string) (string, string) {
	t.Helper()
	modelPath := prefix + ".gguf"
	if err := os.WriteFile(filepath.Join(modelsDir, modelPath), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: prefix, GGUFPath: modelPath})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	instance, err := instanceService.Create(ctx, instances.CreateInput{ModelID: model.ID, Name: prefix, Slug: prefix, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	jobID := prefix + "-job"
	importID := prefix + "-import"
	if _, err := db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state) VALUES(?,?,?,?,?,?,?)`,
		jobID, "huggingface", "acme/demo", "rev", prefix+"-artifact", modelPath, downloads.StateCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,start_attempted) VALUES(?,?,?,?,0,1,?,0)`,
		importID, jobID, model.ID, instance.ID, StateDownloading); err != nil {
		t.Fatal(err)
	}
	return importID, instance.ID
}

func assertStartAttemptClaimed(t *testing.T, ctx context.Context, db database.Store, importID string) {
	t.Helper()
	var attempted int
	var state string
	if err := db.QueryRowContext(ctx, `SELECT start_attempted,state FROM provider_imports WHERE id=?`, importID).Scan(&attempted, &state); err != nil {
		t.Fatal(err)
	}
	if attempted != 1 {
		t.Fatalf("start_attempted=%d want=1", attempted)
	}
	if state != StateCompleted {
		t.Fatalf("state=%q want=%q", state, StateCompleted)
	}
}

func TestReconcileAutoStartClaimedOnceUnderSQLiteConcurrency(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	importID, _ := seedReadyAutoStartImport(t, ctx, modelsDir, db, modelService, service.instances, "ready-autostart-sqlite")

	const workers = 8
	service.store = &preparedBarrierStore{Store: service.store, gate: newPreparedBarrierGate(workers)}
	starter := &starterSpy{}
	service.starter = starter

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- service.Reconcile(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if starter.count() != 1 {
		t.Fatalf("starter calls=%d want=1", starter.count())
	}
	assertStartAttemptClaimed(t, ctx, db, importID)
}

func TestReconcileAutoStartClaimedOnceUnderPostgresConcurrency(t *testing.T) {
	dsn := postgresModelImportDSN(t)
	ctx := context.Background()
	modelsDir := t.TempDir()

	setupStore, err := database.OpenConfigured(ctx, "", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = setupStore.Close() })
	modelService := models.New(setupStore, modelsDir)
	instanceService := instances.NewWithStore(instances.NewInstanceStore(setupStore))
	importID, _ := seedReadyAutoStartImport(t, ctx, modelsDir, setupStore, modelService, instanceService, "ready-autostart-postgres")

	const workers = 8
	gate := newPreparedBarrierGate(workers)
	starter := &starterSpy{}
	services := make([]*Service, 0, workers)
	for range workers {
		store, err := database.OpenConfigured(ctx, "", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		services = append(services, NewWithStores(
			&preparedBarrierStore{Store: NewStore(store), gate: gate},
			instances.NewWithStore(instances.NewInstanceStore(store)),
			modelsDir,
			models.New(store, modelsDir),
			nil,
			starter,
		))
	}

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for _, service := range services {
		service := service
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- service.Reconcile(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if starter.count() != 1 {
		t.Fatalf("starter calls=%d want=1", starter.count())
	}
	assertStartAttemptClaimed(t, ctx, setupStore, importID)
}

func postgresModelImportDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "llamarack_modelimports_" + hex.EncodeToString(random[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
