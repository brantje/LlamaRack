package storagecontract_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/observability"
	"github.com/brantje/llamarack/backend/internal/settings"
	"github.com/brantje/llamarack/backend/internal/supervisor"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type backendFactory struct {
	name string
	open func(t *testing.T) database.Store
}

func TestSQLitePersistenceContract(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manager.db")
	runPersistenceContract(t, backendFactory{
		name: "sqlite",
		open: func(t *testing.T) database.Store {
			t.Helper()
			store, err := database.OpenConfigured(context.Background(), path, "")
			if err != nil {
				t.Fatal(err)
			}
			return store
		},
	}, filepath.Join(root, "models"))
}

func TestPostgresPersistenceContract(t *testing.T) {
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	dsn := isolatedPostgresDSN(t, base)
	runPersistenceContract(t, backendFactory{
		name: "postgres",
		open: func(t *testing.T) database.Store {
			t.Helper()
			store, err := database.OpenConfigured(context.Background(), "", dsn)
			if err != nil {
				t.Fatal(err)
			}
			return store
		},
	}, t.TempDir())
}

func runPersistenceContract(t *testing.T, backend backendFactory, modelsDir string) {
	t.Helper()
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := backend.open(t)
	secretsDir := t.TempDir()

	defaults := settings.Defaults{
		SessionLifetime: 24 * time.Hour,
		AllowedOrigins: "http://localhost:3000",
		StartupTimeout: 180 * time.Second,
		AlwaysOnReconcile: 15 * time.Second,
		DataDir: secretsDir,
		ModelsDir: modelsDir,
		DatabasePath: "contract.db",
		ListenAddr: ":8000",
		LlamaServerPath: "llama-server",
	}
	settingService := settings.New(store, defaults)
	if _, err := settingService.Set(ctx, settings.IdleUnloadSeconds, 321); err != nil {
		t.Fatalf("%s settings write: %v", backend.name, err)
	}

	authService := auth.New(store, time.Hour)
	admin, err := authService.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatalf("%s bootstrap: %v", backend.name, err)
	}
	key, secret, err := authService.CreateAPIKeyForUser(ctx, "contract", admin.ID)
	if err != nil {
		t.Fatalf("%s api key create: %v", backend.name, err)
	}
	if err := authService.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatalf("%s api key authenticate: %v", backend.name, err)
	}
	if err := authService.SetAPIKeyEnabled(ctx, key.ID, false); err != nil {
		t.Fatalf("%s api key disable: %v", backend.name, err)
	}
	if err := authService.AuthenticateAPIKey(ctx, secret); !errors.Is(err, auth.ErrAPIKeyInvalid) {
		t.Fatalf("%s disabled api key remained valid: %v", backend.name, err)
	}
	if err := authService.SetAPIKeyEnabled(ctx, key.ID, true); err != nil {
		t.Fatalf("%s api key re-enable: %v", backend.name, err)
	}

	serviceAccount, err := authService.CreateServiceAccount(ctx, "Contract Service", admin.ID)
	if err != nil {
		t.Fatalf("%s service account create: %v", backend.name, err)
	}
	_, serviceSecret, err := authService.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "contract-service-key", OwnerServiceAccountID: serviceAccount.ID,
	})
	if err != nil {
		t.Fatalf("%s service account api key create: %v", backend.name, err)
	}
	if err := authService.AuthenticateAPIKey(ctx, serviceSecret); err != nil {
		t.Fatalf("%s service account api key authenticate: %v", backend.name, err)
	}

	secretStore, err := huggingface.NewSecretStore(store, secretsDir)
	if err != nil {
		t.Fatalf("%s provider secret store: %v", backend.name, err)
	}
	if err := secretStore.SetToken(ctx, "hf_contract_token"); err != nil {
		t.Fatalf("%s provider token write: %v", backend.name, err)
	}
	if token, err := secretStore.GetToken(ctx); err != nil || token != "hf_contract_token" {
		t.Fatalf("%s provider token=%q err=%v", backend.name, token, err)
	}
	oidcSecret := "contract-oidc-secret"
	oidcManager := auth.NewOIDCManager(authService, settingService, secretStore)
	oidcProvider, err := oidcManager.CreateProvider(ctx, auth.OIDCProviderInput{
		Name: "Contract OIDC", Enabled: true, Issuer: "https://id.example.test",
		ClientID: "contract-client", ClientSecret: &oidcSecret, Scopes: []string{"openid", "profile"},
		AuthorizationEndpoint: "https://id.example.test/authorize",
		TokenEndpoint: "https://id.example.test/token",
		JWKSURL: "https://id.example.test/jwks",
	})
	if err != nil {
		t.Fatalf("%s oidc provider create: %v", backend.name, err)
	}
	if providers, err := oidcManager.ListProviders(ctx); err != nil || len(providers) != 1 || providers[0].ID != oidcProvider.ID || !providers[0].SecretConfigured {
		t.Fatalf("%s oidc providers=%+v err=%v", backend.name, providers, err)
	}

	modelPath := writeContractGGUF(t, modelsDir, "contract-Q4_K_M.gguf")
	modelService := models.New(store, modelsDir)
	model, err := modelService.Create(ctx, models.CreateModelInput{
		Name: "Contract Model", GGUFPath: modelPath, ContextLength: 4096,
		Options: map[string]string{"ctx-size": "4096"},
	})
	if err != nil {
		t.Fatalf("%s model create: %v", backend.name, err)
	}

	instanceService := instances.New(store)
	instance, err := instanceService.Create(ctx, instances.CreateInput{
		ModelID: model.ID,
		Name: "Contract Instance",
		Options: map[string]string{"threads": "4"},
	})
	if err != nil {
		t.Fatalf("%s instance create: %v", backend.name, err)
	}
	if opts, err := instanceService.Options(ctx, instance.ID); err != nil || opts["threads"] != "4" {
		t.Fatalf("%s instance options=%v err=%v", backend.name, opts, err)
	}
	if summary, err := modelService.GGUFSummary(ctx, modelPath); err != nil || summary.Derived.Architecture != "llama" || summary.Derived.ContextLength != 4096 {
		t.Fatalf("%s GGUF summary=%+v err=%v", backend.name, summary, err)
	}
	var indexed int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM gguf_index WHERE path=?`, filepath.Base(modelPath)).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("%s GGUF index count=%d err=%v", backend.name, indexed, err)
	}

	runtimeStore := supervisor.NewSQLStore(store)
	record := supervisor.WorkerRecord{InstanceID: instance.ID, Generation: "generation-a", PID: 1234, StartTicks: 5678, Port: 10001}
	if err := runtimeStore.Upsert(ctx, record); err != nil {
		t.Fatalf("%s runtime upsert: %v", backend.name, err)
	}
	if got, err := runtimeStore.Get(ctx, instance.ID); err != nil || got != record {
		t.Fatalf("%s runtime get=%+v err=%v", backend.name, got, err)
	}

	if _, err := store.ExecContext(ctx, `INSERT INTO download_jobs(
		id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,unixepoch(),unixepoch())`,
		"contract-download", "huggingface", "acme/demo", "revision-a", "artifact-a", "contract.gguf", "Q4_K_M", downloads.StateCompleted, 42, 42, 0, ""); err != nil {
		t.Fatalf("%s download insert: %v", backend.name, err)
	}
	if _, err := store.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path)
		VALUES(?,?,?,?,?,?,?)`, "contract-download", "contract.gguf", 42, downloads.StateCompleted, 42, 0, "contract.gguf"); err != nil {
		t.Fatalf("%s download file insert: %v", backend.name, err)
	}
	downloadManager := downloads.New(ctx, store, modelsDir, nil)
	if got, err := downloadManager.Get(ctx, "contract-download"); err != nil || got.ID != "contract-download" || len(got.Files) != 1 {
		t.Fatalf("%s download get=%+v err=%v", backend.name, got, err)
	}

	observabilityService := observability.New(store)
	now := time.Now().UnixMilli()
	if err := observabilityService.RecordRequest(ctx, observability.RequestRecord{
		StartedAt: now, FinishedAt: now + 5, InstanceID: instance.ID, Endpoint: "/v1/chat/completions",
		StatusCode: 200, Result: "success", DurationMS: 5, PromptTokens: 3, GeneratedTokens: 4, TotalTokens: 7,
	}); err != nil {
		t.Fatalf("%s observability write: %v", backend.name, err)
	}
	if rows, err := observabilityService.ListRequests(ctx, observability.RequestFilters{InstanceID: instance.ID, Limit: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("%s observability rows=%d err=%v", backend.name, len(rows), err)
	}

	mixedRecord := observability.RequestRecord{
		StartedAt: now + 10, FinishedAt: now + 15, InstanceID: "Instance-MiXeD", Endpoint: "/V1/MiXeD",
		TraceID: "TrAcE-MiXeD", StatusCode: 200, Result: "success", DurationMS: 5,
		APIKey: &observability.APIKeyRef{ID: "Key-ID-MiXeD", Name: "Key-Name-MiXeD", Prefix: "sk-MiXeD"},
		ClientIP: "Client-MiXeD", UserAgent: "Agent-MiXeD",
	}
	if err := observabilityService.FinalizeCorrelatedRequest(ctx, "Req-MiXeD", nil, mixedRecord); err != nil {
		t.Fatalf("%s mixed observability write: %v", backend.name, err)
	}
	for _, term := range []string{"req-mixed", "trace-mixed", "instance-mixed", "/v1/mixed", "key-name-mixed", "client-mixed"} {
		rows, err := observabilityService.ListRequests(ctx, observability.RequestFilters{Search: term, Limit: 10})
		if err != nil || len(rows) != 1 || rows[0].RequestID != "Req-MiXeD" {
			t.Fatalf("%s case-insensitive search %q rows=%+v err=%v", backend.name, term, rows, err)
		}
	}
	page, err := observabilityService.ListRequests(ctx, observability.RequestFilters{Limit: 1})
	if err != nil || len(page) != 1 || page[0].RequestID != "Req-MiXeD" {
		t.Fatalf("%s first ordered page=%+v err=%v", backend.name, page, err)
	}
	page, err = observabilityService.ListRequests(ctx, observability.RequestFilters{Limit: 1, Offset: 1})
	if err != nil || len(page) != 1 || page[0].InstanceID != instance.ID {
		t.Fatalf("%s second ordered page=%+v err=%v", backend.name, page, err)
	}

	tx, err := database.Begin(ctx, store)
	if err != nil {
		t.Fatalf("%s begin rollback transaction: %v", backend.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)`, "contract_rollback", "value", time.Now().Unix()); err != nil {
		t.Fatalf("%s rollback insert: %v", backend.name, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("%s rollback: %v", backend.name, err)
	}
	var rolledBack string
	if err := store.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=?`, "contract_rollback").Scan(&rolledBack); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%s rollback persisted row: value=%q err=%v", backend.name, rolledBack, err)
	}

	tx, err = database.Begin(ctx, store)
	if err != nil {
		t.Fatalf("%s begin commit transaction: %v", backend.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)`, "contract_commit", "value", time.Now().Unix()); err != nil {
		t.Fatalf("%s commit insert: %v", backend.name, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("%s commit: %v", backend.name, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("%s close before reopen: %v", backend.name, err)
	}
	store = backend.open(t)
	defer store.Close()

	settingService = settings.New(store, defaults)
	if got, err := settingService.Int(ctx, settings.IdleUnloadSeconds); err != nil || got != 321 {
		t.Fatalf("%s reopened settings=%d err=%v", backend.name, got, err)
	}
	authService = auth.New(store, time.Hour)
	if err := authService.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatalf("%s reopened api key auth: %v", backend.name, err)
	}
	if err := authService.AuthenticateAPIKey(ctx, serviceSecret); err != nil {
		t.Fatalf("%s reopened service account api key auth: %v", backend.name, err)
	}
	if got, err := authService.GetServiceAccount(ctx, serviceAccount.ID); err != nil || got.ID != serviceAccount.ID {
		t.Fatalf("%s reopened service account=%+v err=%v", backend.name, got, err)
	}
	secretStore, err = huggingface.NewSecretStore(store, secretsDir)
	if err != nil {
		t.Fatalf("%s reopened provider secret store: %v", backend.name, err)
	}
	if token, err := secretStore.GetToken(ctx); err != nil || token != "hf_contract_token" {
		t.Fatalf("%s reopened provider token=%q err=%v", backend.name, token, err)
	}
	oidcManager = auth.NewOIDCManager(authService, settingService, secretStore)
	if got, err := oidcManager.GetProvider(ctx, oidcProvider.ID); err != nil || got.ID != oidcProvider.ID || !got.SecretConfigured {
		t.Fatalf("%s reopened oidc provider=%+v err=%v", backend.name, got, err)
	}
	modelService = models.New(store, modelsDir)
	if got, err := modelService.GetByID(ctx, model.ID); err != nil || got.ID != model.ID {
		t.Fatalf("%s reopened model=%+v err=%v", backend.name, got, err)
	}
	if summary, err := modelService.GGUFSummary(ctx, modelPath); err != nil || summary.Derived.Architecture != "llama" {
		t.Fatalf("%s reopened GGUF summary=%+v err=%v", backend.name, summary, err)
	}
	instanceService = instances.New(store)
	if got, err := instanceService.GetByID(ctx, instance.ID); err != nil || got.ID != instance.ID {
		t.Fatalf("%s reopened instance=%+v err=%v", backend.name, got, err)
	}
	runtimeStore = supervisor.NewSQLStore(store)
	if got, err := runtimeStore.Get(ctx, instance.ID); err != nil || got != record {
		t.Fatalf("%s reopened runtime=%+v err=%v", backend.name, got, err)
	}
	downloadManager = downloads.New(ctx, store, modelsDir, nil)
	if got, err := downloadManager.Get(ctx, "contract-download"); err != nil || got.DownloadedBytes != 42 {
		t.Fatalf("%s reopened download=%+v err=%v", backend.name, got, err)
	}
	observabilityService = observability.New(store)
	if rows, err := observabilityService.ListRequests(ctx, observability.RequestFilters{InstanceID: instance.ID, Limit: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("%s reopened observability rows=%d err=%v", backend.name, len(rows), err)
	}

	if err := modelService.Delete(ctx, model.ID); err != nil {
		t.Fatalf("%s model delete: %v", backend.name, err)
	}
	if _, err := instanceService.GetByID(ctx, instance.ID); err == nil {
		t.Fatalf("%s model delete did not cascade to instance", backend.name)
	}
}

func isolatedPostgresDSN(t *testing.T, base string) string {
	t.Helper()
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "llamarack_contract_" + hex.EncodeToString(random[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
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

func writeContractGGUF(t testing.TB, dir, name string) string {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	writeContractBinary(t, &buf, uint32(3))
	writeContractBinary(t, &buf, uint64(0))
	writeContractBinary(t, &buf, uint64(2))
	writeContractString(t, &buf, "general.architecture")
	writeContractBinary(t, &buf, uint32(8))
	writeContractString(t, &buf, "llama")
	writeContractString(t, &buf, "llama.context_length")
	writeContractBinary(t, &buf, uint32(4))
	writeContractBinary(t, &buf, uint32(4096))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeContractString(t testing.TB, buf *bytes.Buffer, value string) {
	t.Helper()
	writeContractBinary(t, buf, uint64(len(value)))
	_, _ = buf.WriteString(value)
}

func writeContractBinary(t testing.TB, buf *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}


func TestSQLiteUserStoreContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	store, err := database.OpenConfigured(context.Background(), path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runUserStoreContract(t, auth.NewUserStore(store))
}

func TestPostgresUserStoreContract(t *testing.T) {
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	store, err := database.OpenConfigured(context.Background(), "", isolatedPostgresDSN(t, base))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runUserStoreContract(t, auth.NewUserStore(store))
}

func runUserStoreContract(t *testing.T, users auth.UserStore) {
	t.Helper()
	ctx := context.Background()
	required, err := users.BootstrapRequired(ctx)
	if err != nil || !required {
		t.Fatalf("bootstrap required=%v err=%v", required, err)
	}
	admin, err := users.Bootstrap(ctx, "Admin", "hash-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.Bootstrap(ctx, "Other", "hash-b", 2); !errors.Is(err, auth.ErrBootstrapCompleted) {
		t.Fatalf("second bootstrap err=%v", err)
	}
	second, err := users.Create(ctx, "Second", "hash-b", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SetEnabled(ctx, admin.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := users.SetEnabled(ctx, second.ID, false); !errors.Is(err, auth.ErrLastEnabledUser) {
		t.Fatalf("last enabled disable err=%v", err)
	}
	if err := users.SetEnabled(ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := users.Delete(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := users.ByID(ctx, admin.ID); err != nil || !got.Enabled || got.Username != "Admin" {
		t.Fatalf("admin=%+v err=%v", got, err)
	}
}

func TestPostgresManagementUserConcurrencyInvariants(t *testing.T) {
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}

	t.Run("bootstrap", func(t *testing.T) {
		store, err := database.OpenConfigured(context.Background(), "", isolatedPostgresDSN(t, base))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		users := auth.NewUserStore(store)
		start := make(chan struct{})
		errs := make(chan error, 2)
		for i, name := range []string{"admin-a", "admin-b"} {
			i, name := i, name
			go func() {
				<-start
				_, err := users.Bootstrap(context.Background(), name, "hash-"+string(rune('a'+i)), int64(i+1))
				errs <- err
			}()
		}
		close(start)
		var success, completed int
		for range 2 {
			err := <-errs
			switch {
			case err == nil:
				success++
			case errors.Is(err, auth.ErrBootstrapCompleted):
				completed++
			default:
				t.Fatalf("bootstrap err=%v", err)
			}
		}
		if success != 1 || completed != 1 {
			t.Fatalf("success=%d completed=%d", success, completed)
		}
		items, err := users.List(context.Background(), 0)
		if err != nil || len(items) != 1 {
			t.Fatalf("users=%v err=%v", items, err)
		}
	})

	for _, tc := range []struct {
		name string
		left func(auth.UserStore, int64) error
		right func(auth.UserStore, int64) error
	}{
		{
			name: "disable-disable",
			left: func(s auth.UserStore, id int64) error { return s.SetEnabled(context.Background(), id, false) },
			right: func(s auth.UserStore, id int64) error { return s.SetEnabled(context.Background(), id, false) },
		},
		{
			name: "delete-delete",
			left: func(s auth.UserStore, id int64) error { return s.Delete(context.Background(), id) },
			right: func(s auth.UserStore, id int64) error { return s.Delete(context.Background(), id) },
		},
		{
			name: "delete-disable",
			left: func(s auth.UserStore, id int64) error { return s.Delete(context.Background(), id) },
			right: func(s auth.UserStore, id int64) error { return s.SetEnabled(context.Background(), id, false) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := database.OpenConfigured(context.Background(), "", isolatedPostgresDSN(t, base))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			users := auth.NewUserStore(store)
			actor, err := users.Bootstrap(context.Background(), "actor", "hash", 1)
			if err != nil {
				t.Fatal(err)
			}
			left, err := users.Create(context.Background(), "left", "hash", 2)
			if err != nil {
				t.Fatal(err)
			}
			right, err := users.Create(context.Background(), "right", "hash", 3)
			if err != nil {
				t.Fatal(err)
			}
			if err := users.SetEnabled(context.Background(), actor.ID, false); err != nil {
				t.Fatal(err)
			}

			start := make(chan struct{})
			errs := make(chan error, 2)
			go func() { <-start; errs <- tc.left(users, left.ID) }()
			go func() { <-start; errs <- tc.right(users, right.ID) }()
			close(start)
			var success, protected int
			for range 2 {
				err := <-errs
				switch {
				case err == nil:
					success++
				case errors.Is(err, auth.ErrLastEnabledUser):
					protected++
				default:
					t.Fatalf("mutation err=%v", err)
				}
			}
			if success != 1 || protected != 1 {
				t.Fatalf("success=%d protected=%d", success, protected)
			}
			items, err := users.List(context.Background(), 0)
			if err != nil {
				t.Fatal(err)
			}
			enabled := 0
			for _, item := range items {
				if item.Enabled {
					enabled++
				}
			}
			if enabled != 1 {
				t.Fatalf("enabled users=%d items=%+v", enabled, items)
			}
		})
	}
}
