package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/internal/auth"
	"github.com/brantje/llamacpp-manager/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/internal/models"
)

const sessionCookie = "lcm_session"

type Server struct {
	auth      *auth.Service
	models    *models.Service
	lifecycle *lifecycle.Service
	profile   func() (llamacpp.Profile, error)
}

func New(authService *auth.Service, modelService *models.Service, lifecycleService *lifecycle.Service, profile func() (llamacpp.Profile,error)) *Server {
	return &Server{auth:authService,models:modelService,lifecycle:lifecycleService,profile:profile}
}

func (s *Server) ServeHTTP(w http.ResponseWriter,r *http.Request){
	path:=strings.TrimSuffix(r.URL.Path,"/")
	if path==""{path="/"}
	switch {
	case path=="/api/v1/health" && r.Method==http.MethodGet:
		writeJSON(w,200,map[string]any{"status":"ok"})
	case path=="/api/v1/auth/bootstrap" && r.Method==http.MethodGet:
		required,err:=s.auth.BootstrapRequired(r.Context());if err!=nil{writeErr(w,500,err);return};writeJSON(w,200,map[string]bool{"required":required})
	case path=="/api/v1/auth/bootstrap" && r.Method==http.MethodPost:
		s.bootstrap(w,r)
	case path=="/api/v1/auth/login" && r.Method==http.MethodPost:
		s.login(w,r)
	case path=="/api/v1/auth/logout" && r.Method==http.MethodPost:
		s.logout(w,r)
	default:
		user,ok:=s.requireUser(w,r);if !ok{return}
		s.serveAuthenticated(w,r,path,user)
	}
}

func(s *Server)serveAuthenticated(w http.ResponseWriter,r *http.Request,path string,user auth.User){
	switch {
	case path=="/api/v1/me" && r.Method==http.MethodGet:
		writeJSON(w,200,user)
	case path=="/api/v1/models" && r.Method==http.MethodGet:
		items,err:=s.models.List(r.Context());if err!=nil{writeErr(w,500,err);return};writeJSON(w,200,items)
	case path=="/api/v1/models" && r.Method==http.MethodPost:
		if !requireOperate(w,user){return};var in models.CreateModelInput;if !decode(w,r,&in){return};item,err:=s.models.Create(r.Context(),in);if err!=nil{writeErr(w,400,err);return};writeJSON(w,201,item)
	case path=="/api/v1/artifacts" && r.Method==http.MethodGet:
		items,err:=s.models.ListArtifacts(r.Context());if err!=nil{writeErr(w,500,err);return};writeJSON(w,200,items)
	case path=="/api/v1/artifacts/register" && r.Method==http.MethodPost:
		if !requireOperate(w,user){return};var in struct{Path string `json:"path"`;DisplayName string `json:"display_name"`};if !decode(w,r,&in){return};item,err:=s.models.RegisterLocalArtifact(r.Context(),in.Path,in.DisplayName);if err!=nil{writeErr(w,400,err);return};writeJSON(w,201,item)
	case path=="/api/v1/llamacpp/profile" && r.Method==http.MethodGet:
		profile,err:=s.profile();if err!=nil{writeJSON(w,503,map[string]any{"available":false,"error":err.Error()});return};writeJSON(w,200,map[string]any{"available":true,"profile":profile})
	case path=="/api/v1/api-keys" && r.Method==http.MethodGet:
		if !auth.IsAdmin(user.Role){writeForbidden(w);return};items,err:=s.auth.ListAPIKeys(r.Context());if err!=nil{writeErr(w,500,err);return};writeJSON(w,200,items)
	case path=="/api/v1/api-keys" && r.Method==http.MethodPost:
		if !auth.IsAdmin(user.Role){writeForbidden(w);return};var in struct{Name string `json:"name"`};if !decode(w,r,&in){return};key,secret,err:=s.auth.CreateAPIKey(r.Context(),in.Name,user.ID);if err!=nil{writeErr(w,500,err);return};writeJSON(w,201,map[string]any{"key":key,"secret":secret})
	case strings.HasPrefix(path,"/api/v1/api-keys/") && strings.HasSuffix(path,"/revoke") && r.Method==http.MethodPost:
		if !auth.IsAdmin(user.Role){writeForbidden(w);return};id:=strings.TrimSuffix(strings.TrimPrefix(path,"/api/v1/api-keys/"),"/revoke");if err:=s.auth.RevokeAPIKey(r.Context(),id);err!=nil{writeErr(w,500,err);return};w.WriteHeader(204)
	case strings.HasPrefix(path,"/api/v1/models/"):
		s.modelSubresource(w,r,path,user)
	case strings.HasPrefix(path,"/api/v1/instances/") && strings.HasSuffix(path,"/logs") && r.Method==http.MethodGet:
		id:=strings.TrimSuffix(strings.TrimPrefix(path,"/api/v1/instances/"),"/logs");writeJSON(w,200,map[string]any{"lines":s.lifecycle.Logs(id)})
	default:
		writeJSON(w,404,map[string]string{"error":"not found"})
	}
}

func(s *Server)modelSubresource(w http.ResponseWriter,r *http.Request,path string,user auth.User){
	rest:=strings.TrimPrefix(path,"/api/v1/models/");parts:=strings.Split(rest,"/");if len(parts)<1||parts[0]==""{writeJSON(w,404,map[string]string{"error":"not found"});return};id:=parts[0]
	if len(parts)==1 && r.Method==http.MethodGet { item,err:=s.models.GetByID(r.Context(),id);if err!=nil{writeErr(w,404,err);return};writeJSON(w,200,item);return }
	if len(parts)!=2 { writeJSON(w,404,map[string]string{"error":"not found"});return }
	switch parts[1] {
	case "start":
		if r.Method!=http.MethodPost||!requireOperate(w,user){return};endpoint,err:=s.lifecycle.StartModel(r.Context(),id);if err!=nil{writeErr(w,503,err);return};writeJSON(w,202,map[string]any{"status":"ready","internal_endpoint":endpoint})
	case "stop":
		if r.Method!=http.MethodPost||!requireOperate(w,user){return};if err:=s.lifecycle.StopModel(r.Context(),id);err!=nil{writeErr(w,500,err);return};w.WriteHeader(204)
	case "runtime":
		if r.Method!=http.MethodGet{return};items,err:=s.lifecycle.RuntimeForModel(r.Context(),id);if err!=nil{writeErr(w,500,err);return};writeJSON(w,200,items)
	case "options":
		if r.Method==http.MethodGet {items,err:=s.models.Options(r.Context(),id);if err!=nil{writeErr(w,500,err);return};writeJSON(w,200,items);return}
		if r.Method==http.MethodPut {if !requireOperate(w,user){return};var options map[string]string;if !decode(w,r,&options){return};if err:=s.models.ReplaceOptions(r.Context(),id,options);err!=nil{writeErr(w,400,err);return};w.WriteHeader(204);return}
		w.WriteHeader(405)
	default: writeJSON(w,404,map[string]string{"error":"not found"})
	}
}

func(s *Server)bootstrap(w http.ResponseWriter,r *http.Request){var in struct{Username string `json:"username"`;Password string `json:"password"`};if !decode(w,r,&in){return};user,err:=s.auth.BootstrapAdmin(r.Context(),in.Username,in.Password);if err!=nil{writeErr(w,400,err);return};writeJSON(w,201,user)}
func(s *Server)login(w http.ResponseWriter,r *http.Request){var in struct{Username string `json:"username"`;Password string `json:"password"`};if !decode(w,r,&in){return};token,user,err:=s.auth.Login(r.Context(),in.Username,in.Password);if err!=nil{writeJSON(w,401,map[string]string{"error":"invalid username or password"});return};http.SetCookie(w,&http.Cookie{Name:sessionCookie,Value:token,Path:"/",HttpOnly:true,SameSite:http.SameSiteLaxMode,MaxAge:int((24*time.Hour).Seconds())});writeJSON(w,200,user)}
func(s *Server)logout(w http.ResponseWriter,r *http.Request){if cookie,err:=r.Cookie(sessionCookie);err==nil{_ = s.auth.Logout(r.Context(),cookie.Value)};http.SetCookie(w,&http.Cookie{Name:sessionCookie,Value:"",Path:"/",HttpOnly:true,SameSite:http.SameSiteLaxMode,MaxAge:-1});w.WriteHeader(204)}
func(s *Server)requireUser(w http.ResponseWriter,r *http.Request)(auth.User,bool){cookie,err:=r.Cookie(sessionCookie);if err!=nil{writeJSON(w,401,map[string]string{"error":"authentication required"});return auth.User{},false};user,err:=s.auth.SessionUser(r.Context(),cookie.Value);if err!=nil{writeJSON(w,401,map[string]string{"error":"authentication required"});return auth.User{},false};return user,true}
func requireOperate(w http.ResponseWriter,user auth.User)bool{if !auth.CanOperate(user.Role){writeForbidden(w);return false};return true}
func writeForbidden(w http.ResponseWriter){writeJSON(w,403,map[string]string{"error":"forbidden"})}
func decode(w http.ResponseWriter,r *http.Request,v any)bool{dec:=json.NewDecoder(http.MaxBytesReader(w,r.Body,2<<20));dec.DisallowUnknownFields();if err:=dec.Decode(v);err!=nil{writeErr(w,400,err);return false};return true}
func writeErr(w http.ResponseWriter,status int,err error){if err==nil{err=errors.New("unknown error")};writeJSON(w,status,map[string]string{"error":err.Error()})}
func writeJSON(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(v)}
