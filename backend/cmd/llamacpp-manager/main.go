package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/api"
	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/config"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/gateway"
	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/backend/internal/llamaconfig"
	"github.com/brantje/llamacpp-manager/backend/internal/modelimports"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
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
		IdleUnloadTimeout: cfg.IdleUnloadTimeout, AlwaysOnReconcile: cfg.AlwaysOnReconcileInterval,
		DataDir: cfg.DataDir, ModelsDir: cfg.ModelsDir, DatabasePath: cfg.DatabasePath, ListenAddr: cfg.ListenAddr, LlamaServerPath: cfg.LlamaServerPath,
	})
	sessionLifetime := cfg.SessionLifetime
	if seconds, resolveErr := managerSettings.Int(ctx, settings.SessionLifetimeSeconds); resolveErr == nil {
		sessionLifetime = time.Duration(seconds) * time.Second
	}
	startupTimeout := cfg.StartupTimeout
	if seconds, resolveErr := managerSettings.Int(ctx, settings.StartupTimeoutSeconds); resolveErr == nil {
		startupTimeout = time.Duration(seconds) * time.Second
	}
	idleUnloadTimeout := cfg.IdleUnloadTimeout
	if seconds, resolveErr := managerSettings.Int(ctx, settings.IdleUnloadSeconds); resolveErr == nil {
		idleUnloadTimeout = time.Duration(seconds) * time.Second
	}
	alwaysOnInterval := cfg.AlwaysOnReconcileInterval
	if seconds, resolveErr := managerSettings.Int(ctx, settings.AlwaysOnReconcileSeconds); resolveErr == nil {
		alwaysOnInterval = time.Duration(seconds) * time.Second
	}

	authService := auth.New(db, sessionLifetime)
	network := managersecurity.NewNetwork(managerSettings)
	loginProtector := managersecurity.NewLoginProtector(managerSettings)
	modelService := models.New(db, cfg.ModelsDir)
	unregisterDetectedDefaults := modelService.RegisterDetectedLlamaDefaults()
	defer unregisterDetectedDefaults()
	sup := supervisor.New(cfg.LlamaServerPath, cfg.WorkerHost, cfg.WorkerPortStart, startupTimeout)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sup.Shutdown(shutdownCtx)
	}()
	lifecycleService := lifecycle.New(modelService, sup)

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
	hfClient, err := huggingface.NewClient(cfg.HuggingFaceBaseURL, providerSecrets.GetToken)
	if err != nil {
		return fmt.Errorf("initialize Hugging Face provider: %w", err)
	}
	downloadManager := downloads.New(ctx, db, cfg.ModelsDir, hfClient)
	importService := modelimports.New(db, cfg.ModelsDir, modelService, downloadManager, lifecycleService)
	if err := downloadManager.ResumePending(ctx); err != nil {
		return fmt.Errorf("resume downloads: %w", err)
	}

	apiServer := api.New(authService, modelService, lifecycleService, profileGetter)
	managementAPI := http.NewServeMux()
	hardwareDetector := hardware.New()
	allowedOrigins, _ := managerSettings.String(ctx, settings.AllowedOrigins)
	managementAPI.Handle("/api/v1/ws", api.NewRuntimeWebSocketHandler(authService, lifecycleService, allowedOrigins))
	managementAPI.Handle("/api/v1/hardware", api.NewPhase7HardwareHandler(authService, hardwareDetector))
	managementAPI.Handle("POST /api/v1/models", api.NewPhase9ModelCreateHandler(apiServer, modelService))
	managementAPI.Handle("POST /api/v1/models/inspect", api.NewPhase9ModelInspectHandler(authService, modelService))
	managementAPI.Handle("GET /api/v1/models/{id}/details/value", api.NewPhase9ModelMetadataValueHandler(authService, modelService))
	managementAPI.Handle("GET /api/v1/models/{id}/details", api.NewPhase9ModelDetailsHandler(authService, modelService))
	managementAPI.Handle("GET /api/v1/models/{id}/recommendation", api.NewPhase9RecommendationHandler(authService, modelService, hardwareDetector))
	managementAPI.Handle("/api/v1/llamacpp/config", api.NewLlamaConfigHandler(authService, llamaconfig.New(db), profileGetter))
	phase8 := api.NewPhase8Handler(authService, hfClient, providerSecrets, downloadManager, importService)
	managementAPI.Handle("/api/v1/huggingface/", phase8)
	managementAPI.Handle("/api/v1/imports", phase8)
	managementAPI.Handle("/api/v1/downloads", phase8)
	managementAPI.Handle("/api/v1/downloads/", phase8)

	phase10Auth := api.NewPhase10AuthHandler(authService, network, loginProtector)
	managementAPI.Handle("/api/v1/auth/", phase10Auth)
	phase10 := api.NewPhase10Handler(authService, managerSettings, providerSecrets, network, profileGetter)
	managementAPI.Handle("GET /api/v1/me", phase10)
	managementAPI.Handle("/api/v1/me/", phase10)
	managementAPI.Handle("/api/v1/users", phase10)
	managementAPI.Handle("/api/v1/users/", phase10)
	managementAPI.Handle("/api/v1/sessions/", phase10)
	managementAPI.Handle("/api/v1/settings/general", phase10)
	managementAPI.Handle("/api/v1/system", phase10)
	managementAPI.Handle("/api/v1/admin/summary", phase10)
	apiKeys := api.NewPhase10APIKeysHandler(authService)
	managementAPI.Handle("/api/v1/api-keys", apiKeys)
	managementAPI.Handle("/api/v1/api-keys/", apiKeys)
	managementAPI.Handle("/", apiServer)

	securedManagement := api.ManagementSecurity(authService, network, managementAPI)
	openAI := gateway.New(authService, modelService, lifecycleService)
	mux := newMux(securedManagement, openAI)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           managersecurity.Headers(network, dynamicCORS(network, mux)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveErr := make(chan error, 1)
	go lifecycleService.RunReconciler(ctx, alwaysOnInterval)
	go lifecycleService.RunIdleReconciler(ctx, idleUnloadTimeout)
	go modelService.RunMetadataReconciler(ctx, 2*time.Second)
	go importService.Run(ctx, 500*time.Millisecond)
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

func newMux(apiServer, openAI http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", apiServer)
	mux.Handle("/v1/", openAI)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "llamacpp-manager", "status": "running"})
	})
	return mux
}

func dynamicCORS(network *managersecurity.Network, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && network.OriginAllowed(r, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// cors and originAllowed remain for focused compatibility tests and callers that need a
// fixed-origin wrapper. Runtime uses dynamicCORS so database settings take effect without restart.
func cors(allowedOrigins string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, r.Host, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin, requestHost, configured string) bool {
	for _, allowed := range strings.Split(configured, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Hostname() == "" || (originURL.Scheme != "http" && originURL.Scheme != "https") {
		return false
	}
	requestURL, err := url.Parse("http://" + requestHost)
	if err != nil || requestURL.Hostname() == "" {
		return false
	}
	return strings.EqualFold(originURL.Hostname(), requestURL.Hostname())
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
