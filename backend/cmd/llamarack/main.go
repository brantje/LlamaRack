package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/brantje/llamarack/backend/internal/api"
	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/config"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	frontendui "github.com/brantje/llamarack/backend/internal/frontend"
	"github.com/brantje/llamarack/backend/internal/gateway"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/litellm"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/modelimports"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/observability"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, config.Load()); err != nil {
		slog.Error("backend failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config) error {
	if err := os.MkdirAll(cfg.ModelsDir, 0o755); err != nil {
		return fmt.Errorf("create models dir: %w", err)
	}
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	managerSettings := settings.New(db, settings.Defaults{
		SessionLifetime: cfg.SessionLifetime, AllowedOrigins: cfg.AllowedOrigin, StartupTimeout: cfg.StartupTimeout,
		AlwaysOnReconcile: cfg.AlwaysOnReconcileInterval,
		DataDir:           cfg.DataDir, ModelsDir: cfg.ModelsDir, DatabasePath: cfg.DatabasePath, ListenAddr: cfg.ListenAddr, LlamaServerPath: cfg.LlamaServerPath,
	})
	sessionLifetime := cfg.SessionLifetime
	if seconds, resolveErr := managerSettings.Int(ctx, settings.SessionLifetimeSeconds); resolveErr == nil {
		sessionLifetime = time.Duration(seconds) * time.Second
	}
	startupTimeout := cfg.StartupTimeout
	if seconds, resolveErr := managerSettings.Int(ctx, settings.StartupTimeoutSeconds); resolveErr == nil {
		startupTimeout = time.Duration(seconds) * time.Second
	}
	idleUnloadTimeout := 5 * time.Minute
	if seconds, resolveErr := managerSettings.Int(ctx, settings.IdleUnloadSeconds); resolveErr == nil {
		idleUnloadTimeout = time.Duration(seconds) * time.Second
	}
	alwaysOnInterval := cfg.AlwaysOnReconcileInterval
	if seconds, resolveErr := managerSettings.Int(ctx, settings.AlwaysOnReconcileSeconds); resolveErr == nil {
		alwaysOnInterval = time.Duration(seconds) * time.Second
	}

	authService := auth.New(db, sessionLifetime)
	if err := authService.UsePersistentSigningKey(cfg.DataDir); err != nil {
		return fmt.Errorf("initialize management signing key: %w", err)
	}
	network := managersecurity.NewNetwork(managerSettings)
	loginProtector := managersecurity.NewLoginProtector(managerSettings)
	modelService := models.New(db, cfg.ModelsDir)
	unregisterDetectedDefaults := modelService.RegisterDetectedLlamaDefaults()
	defer unregisterDetectedDefaults()
	sup := supervisor.New(cfg.LlamaServerPath, cfg.WorkerHost, cfg.WorkerPortStart, startupTimeout)
	installID, err := supervisor.EnsureInstallationID(ctx, db)
	if err != nil {
		return fmt.Errorf("installation identity: %w", err)
	}
	sup.SetRuntimeIdentity(installID, supervisor.NewSQLStore(db))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sup.Shutdown(shutdownCtx)
	}()
	lifecycleService := lifecycle.New(modelService, sup)
	lifecycleService.SetDataDir(cfg.DataDir)
	observabilityService := observability.New(db)
	pendingLimits := func(requestCtx context.Context) (int, int) {
		perInstance, global := 32, 128
		if value, resolveErr := managerSettings.Int(requestCtx, settings.MaxPendingRequestsPerInstance); resolveErr == nil {
			perInstance = value
		}
		if value, resolveErr := managerSettings.Int(requestCtx, settings.MaxPendingRequestsGlobal); resolveErr == nil {
			global = value
		}
		return perInstance, global
	}
	lifecycleService.SetPendingLimits(pendingLimits)
	observabilityService.SetPendingLimits(pendingLimits)
	observabilitySampler := observability.NewSampler(lifecycleService, observabilityService, idleUnloadTimeout)

	var profileMu sync.RWMutex
	var profile llamacpp.Profile
	var profileErr error
	refreshProfile := func() {
		p, err := llamacpp.Discover(context.Background(), cfg.LlamaServerPath)
		profileMu.Lock()
		profile, profileErr = p, err
		profileMu.Unlock()
		if err != nil {
			slog.Warn("llama-server discovery unavailable", "error", err)
		} else {
			slog.Info("llama-server discovered", "version", p.Version, "options", len(p.Options))
		}
	}
	refreshProfile()
	profileGetter := func() (llamacpp.Profile, error) {
		profileMu.RLock()
		defer profileMu.RUnlock()
		return profile, profileErr
	}
	lifecycleService.SetProfileGetter(profileGetter)

	providerSecrets, err := huggingface.NewSecretStore(db, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("initialize provider secrets: %w", err)
	}
	oidcManager := auth.NewOIDCManager(authService, managerSettings, providerSecrets)
	hfClient, err := huggingface.NewClient(cfg.HuggingFaceBaseURL, providerSecrets.GetToken)
	if err != nil {
		return fmt.Errorf("initialize Hugging Face provider: %w", err)
	}
	downloadManager := downloads.New(ctx, db, cfg.ModelsDir, hfClient)
	importService := modelimports.New(db, cfg.ModelsDir, modelService, downloadManager, lifecycleService)
	liteLLMService := litellm.New(db, authService, providerSecrets, managerSettings)
	lifecycleService.Instances().SetOnChange(liteLLMService.NotifyInstanceChange)
	importService.SetInstanceOnChange(liteLLMService.NotifyInstanceChange)
	if err := downloadManager.ResumePending(ctx); err != nil {
		return fmt.Errorf("resume downloads: %w", err)
	}

	apiServer := api.New(modelService, lifecycleService, profileGetter)
	managementAPI := http.NewServeMux()
	hardwareDetector := hardware.New()
	allowedOrigins, _ := managerSettings.String(ctx, settings.AllowedOrigins)
	managementAPI.Handle("/api/v1/ws", api.NewRuntimeWebSocketHandler(authService, lifecycleService, allowedOrigins, observabilitySampler))
	managementAPI.Handle("/api/v1/hardware", api.NewHardwareHandler(authService, hardwareDetector))
	managementAPI.Handle("GET /api/v1/huggingface/recommendations", api.NewDiscoverRecommendationHandler(authService, hfClient, hardwareDetector, managerSettings, profileGetter))
	managementAPI.Handle("/api/v1/settings/discover", api.NewDiscoverSettingsHandler(authService, managerSettings))
	logHandler := api.NewInstanceLogHandler(lifecycleService)
	managementAPI.Handle("/api/v1/logs", logHandler)
	managementAPI.Handle("/api/v1/logs/", logHandler)
	managementAPI.Handle("POST /api/v1/models", api.NewModelCreateHandler(apiServer, modelService))
	managementAPI.Handle("POST /api/v1/models/inspect", api.NewModelInspectHandler(authService, modelService))
	managementAPI.Handle("GET /api/v1/models/{id}/details/value", api.NewModelMetadataValueHandler(authService, modelService))
	managementAPI.Handle("GET /api/v1/models/{id}/details", api.NewModelDetailsHandler(authService, modelService))
	managementAPI.Handle("GET /api/v1/models/{id}/recommendation", api.NewRecommendationHandler(authService, modelService, hardwareDetector, profileGetter))
	managementAPI.Handle("/api/v1/llamacpp/config", api.NewLlamaConfigHandler(authService, llamaconfig.New(db), profileGetter))
	managementAPI.Handle("GET /api/v1/observability/requests", observability.NewRequestLogsHandler(observabilityService))
	managementAPI.Handle("GET /api/v1/observability/requests/{request_id}", observability.NewRequestLogDetailHandler(observabilityService))
	managementAPI.Handle("GET /api/v1/observability/playground/{request_id}", observability.NewPlaygroundDiagnosticsHandler(observabilityService))
	managementAPI.Handle("/api/v1/observability/", observability.NewManagementHandler(observabilityService))
	huggingFace := api.NewHuggingFaceHandler(authService, hfClient, providerSecrets, downloadManager, importService)
	managementAPI.Handle("/api/v1/huggingface/", huggingFace)
	liteLLMHandler := api.NewLiteLLMHandler(authService, liteLLMService)
	managementAPI.Handle("/api/v1/litellm/", liteLLMHandler)
	managementAPI.Handle("/api/v1/litellm", liteLLMHandler)
	managementAPI.Handle("/api/v1/imports", huggingFace)
	managementAPI.Handle("/api/v1/downloads", huggingFace)
	managementAPI.Handle("/api/v1/downloads/", huggingFace)

	authHandler := api.NewAuthHandler(authService, network, loginProtector, managerSettings)
	managementAPI.Handle("/api/v1/auth/", authHandler)
	oidcHandler := api.NewOIDCHandler(oidcManager, authService, managerSettings, network)
	managementAPI.Handle("/api/v1/auth/providers", oidcHandler)
	managementAPI.Handle("/api/v1/auth/oidc/", oidcHandler)
	managementAPI.Handle("/api/v1/auth/ws-ticket", oidcHandler)
	managementAPI.Handle("/api/v1/admin/auth/", oidcHandler)
	managementAPI.Handle("/api/v1/me/identities", oidcHandler)
	managementAPI.Handle("/api/v1/me/identities/", oidcHandler)
	adminHandler := api.NewAdminHandler(authService, managerSettings, providerSecrets, network, profileGetter, liteLLMService)
	managementAPI.Handle("GET /api/v1/me", adminHandler)
	managementAPI.Handle("/api/v1/me/", adminHandler)
	managementAPI.Handle("/api/v1/users", adminHandler)
	managementAPI.Handle("/api/v1/users/", adminHandler)
	managementAPI.Handle("/api/v1/sessions/", adminHandler)
	managementAPI.Handle("/api/v1/settings/general", adminHandler)
	managementAPI.Handle("/api/v1/system", adminHandler)
	managementAPI.Handle("/api/v1/admin/summary", adminHandler)
	apiKeys := api.NewAPIKeysHandler(authService)
	managementAPI.Handle("/api/v1/api-keys", apiKeys)
	managementAPI.Handle("/api/v1/api-keys/", apiKeys)
	serviceAccounts := api.NewServiceAccountsHandler(authService)
	managementAPI.Handle("/api/v1/admin/service-accounts", serviceAccounts)
	managementAPI.Handle("/api/v1/admin/service-accounts/", serviceAccounts)
	managementAPI.Handle("/", apiServer)

	securedManagement := api.ManagementSecurity(authService, network, managementAPI)
	openAI := gateway.WithRequestLogContext(gateway.New(authService, modelService, lifecycleService, observabilityService), observabilityService)
	managementAPI.Handle("POST /api/v1/playground/chat/completions", gateway.NewManagementPlaygroundProxy(openAI))
	metrics := observability.NewMetricsHandler(observabilityService, func(requestCtx context.Context) string {
		value, resolveErr := managerSettings.String(requestCtx, settings.PrometheusAuthToken)
		if resolveErr != nil {
			return ""
		}
		return value
	}, observabilitySampler.RuntimeStates)
	frontendDir := os.Getenv("LLAMARACK_FRONTEND_DIR")
	if frontendDir == "" {
		frontendDir = "/app/frontend"
	}
	mux := newMux(securedManagement, openAI, frontendui.New(frontendDir), metrics)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           managersecurity.Headers(network, dynamicCORS(network, mux)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveErr := make(chan error, 1)
	lifecycleService.ArmStartupReconcile()
	if err := lifecycleService.ReconcileStaleWorkers(ctx); err != nil {
		slog.Error("startup worker reconciliation failed", "error", err)
	}
	go lifecycleService.RunReconciler(ctx, alwaysOnInterval)
	go lifecycleService.RunIdleReconciler(ctx, idleUnloadTimeout)
	go func() { liteLLMService.BootReconcile(ctx) }()
	go modelService.RunMetadataReconciler(ctx, 2*time.Second)
	go importService.Run(ctx, 500*time.Millisecond)
	go observabilitySampler.Run(ctx)
	retentionDays := func(requestCtx context.Context) int {
		value, resolveErr := managerSettings.Int(requestCtx, settings.ObservabilityRetentionDays)
		if resolveErr != nil {
			return observability.DefaultRetentionDays
		}
		return value
	}
	go observabilityService.RunRetention(ctx, retentionDays)
	go observabilityService.RunHardwareRetention(ctx, retentionDays)
	go func() {
		slog.Info("backend listening", "addr", cfg.ListenAddr)
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

func newMux(apiServer, openAI, frontendHandler http.Handler, metrics ...http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	openAPIDoc := newOpenAPIDocument()
	mux.Handle("GET /openapi.json", openAPIDoc.JSONHandler())
	mux.Handle("GET /docs", openAPIDoc.DocsHandler("/openapi.json"))
	mux.Handle("/api/v1/", apiServer)
	mux.Handle("/v1/", openAI)
	if len(metrics) > 0 && metrics[0] != nil {
		mux.Handle("/metrics", metrics[0])
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.Handle("/", frontendHandler)
	return mux
}

func dynamicCORS(network *managersecurity.Network, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && network.OriginAllowed(r, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-LiteLLM-Trace-ID, X-LiteLLM-Session-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", gateway.CORSExposeHeaders())
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func healthcheck() {
	if err := checkHealth("http://127.0.0.1:8000/health"); err != nil {
		os.Exit(1)
	}
}

func checkHealth(endpoint string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}
