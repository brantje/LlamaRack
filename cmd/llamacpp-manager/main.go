package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/brantje/llamacpp-manager/internal/api"
	"github.com/brantje/llamacpp-manager/internal/auth"
	"github.com/brantje/llamacpp-manager/internal/config"
	"github.com/brantje/llamacpp-manager/internal/database"
	"github.com/brantje/llamacpp-manager/internal/downloads"
	"github.com/brantje/llamacpp-manager/internal/gateway"
	"github.com/brantje/llamacpp-manager/internal/hardware"
	"github.com/brantje/llamacpp-manager/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/internal/models"
	"github.com/brantje/llamacpp-manager/internal/providers"
	"github.com/brantje/llamacpp-manager/internal/router"
	"github.com/brantje/llamacpp-manager/internal/scheduler"
	"github.com/brantje/llamacpp-manager/internal/secrets"
	"github.com/brantje/llamacpp-manager/internal/supervisor"
)

func main(){
	ctx,stop:=signal.NotifyContext(context.Background(),syscall.SIGINT,syscall.SIGTERM);defer stop()
	cfg,err:=config.Load();if err!=nil{slog.Error("load config","error",err);os.Exit(1)}
	db,err:=database.Open(ctx,cfg.DatabasePath);if err!=nil{slog.Error("open database","error",err);os.Exit(1)};defer db.Close()
	secretStore,err:=secrets.New(db.DB,cfg.DataDir);if err!=nil{slog.Error("initialize secret store","error",err);os.Exit(1)}

	authService:=auth.New(db.DB,cfg.SessionLifetime)
	modelService:=models.New(db.DB,cfg.ModelsDir)
	sup:=supervisor.New(cfg.LlamaServerPath,cfg.WorkerHost,cfg.WorkerPortStart,cfg.StartupTimeout)
	requestRouter:=router.New(modelService,sup)
	hardwareService:=hardware.New()
	schedulerService:=scheduler.New(hardwareService,modelService,sup,requestRouter)
	lifecycleService:=lifecycle.New(modelService,sup,schedulerService,requestRouter,cfg.IdleTimeout)

	hfToken:=os.Getenv("HF_TOKEN")
	if stored,ok,err:=secretStore.Get(ctx,"huggingface_token");err==nil&&ok{hfToken=stored}else if err!=nil{slog.Warn("read Hugging Face token","error",err)}
	hfProvider:=providers.NewHuggingFace(hfToken)
	downloadManager:=downloads.New(cfg.ModelsDir,modelService,hfProvider)

	var profileMu sync.RWMutex;var profile llamacpp.Profile;var profileErr error
	refreshProfile:=func(){p,err:=llamacpp.NewDiscoverer(cfg.LlamaServerPath).Discover(context.Background());profileMu.Lock();profile,profileErr=p,err;profileMu.Unlock();if err!=nil{slog.Warn("llama-server capability discovery failed","error",err)}else{slog.Info("llama-server discovered","version",p.Version,"options",len(p.Options))}}
	refreshProfile();profileGetter:=func()(llamacpp.Profile,error){profileMu.RLock();defer profileMu.RUnlock();return profile,profileErr}

	apiServer:=api.New(authService,modelService,lifecycleService,hfProvider,downloadManager,hardwareService,secretStore,profileGetter)
	openAIGateway:=gateway.New(authService,modelService,lifecycleService,requestRouter)
	mux:=http.NewServeMux();mux.Handle("/api/v1/",apiServer);mux.Handle("/v1/",openAIGateway);mux.HandleFunc("/metrics",func(w http.ResponseWriter,_ *http.Request){w.Header().Set("Content-Type","text/plain; version=0.0.4");_,_=w.Write([]byte("# HELP llamacpp_manager_up Whether the manager is running.\n# TYPE llamacpp_manager_up gauge\nllamacpp_manager_up 1\n"))});mux.Handle("/",frontendHandler())
	server:=&http.Server{Addr:cfg.ListenAddr,Handler:securityHeaders(mux),ReadHeaderTimeout:10*time.Second,IdleTimeout:2*time.Minute}
	go lifecycleService.RunReconciler(ctx,15*time.Second)
	go func(){slog.Info("llamacpp-manager listening","addr",cfg.ListenAddr);if err:=server.ListenAndServe();err!=nil&&!errors.Is(err,http.ErrServerClosed){slog.Error("http server failed","error",err);stop()}}()
	<-ctx.Done();shutdownCtx,cancel:=context.WithTimeout(context.Background(),cfg.ShutdownTimeout);defer cancel();_ = server.Shutdown(shutdownCtx);sup.Shutdown(shutdownCtx)
}

func frontendHandler()http.Handler{root:=os.Getenv("LCM_WEB_ROOT");if root==""{root="/app/web"};if info,err:=os.Stat(root);err==nil&&info.IsDir(){files:=http.FileServer(http.Dir(root));return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){relative:=strings.TrimPrefix(filepath.Clean("/"+r.URL.Path),"/");candidate:=filepath.Join(root,relative);if _,err:=os.Stat(candidate);err!=nil{http.ServeFile(w,r,filepath.Join(root,"index.html"));return};files.ServeHTTP(w,r)})};return http.HandlerFunc(func(w http.ResponseWriter,_ *http.Request){w.Header().Set("Content-Type","text/plain; charset=utf-8");w.WriteHeader(http.StatusServiceUnavailable);_,_=w.Write([]byte("llamacpp-manager API is running; web assets are not installed\n"))})}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("X-Frame-Options","DENY");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("Content-Security-Policy","default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'");next.ServeHTTP(w,r)})}
