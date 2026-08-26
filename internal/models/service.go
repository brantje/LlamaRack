package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Priority string

type Model struct {
	ID string `json:"id"`
	ModelID string `json:"model_id"`
	DisplayName string `json:"display_name,omitempty"`
	ArtifactID string `json:"artifact_id"`
	ArtifactPath string `json:"artifact_path,omitempty"`
	Enabled bool `json:"enabled"`
	AutoloadEnabled bool `json:"autoload_enabled"`
	AlwaysOn bool `json:"always_on"`
	IdleTimeoutSeconds *int64 `json:"idle_timeout_seconds,omitempty"`
	StartupTimeoutSeconds *int64 `json:"startup_timeout_seconds,omitempty"`
	Priority Priority `json:"priority"`
	RoutingPolicy string `json:"routing_policy"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type Artifact struct {
	ID string `json:"id"`
	DisplayName string `json:"display_name"`
	LocalPath string `json:"local_path"`
	TotalBytes int64 `json:"total_bytes"`
	Quantization string `json:"quantization,omitempty"`
	Provider string `json:"provider"`
	Source string `json:"source,omitempty"`
	Revision string `json:"revision,omitempty"`
	Completed bool `json:"completed"`
	CreatedAt string `json:"created_at,omitempty"`
}

type Instance struct {
	ID string `json:"id"`
	ModelID string `json:"model_id"`
	Name string `json:"name"`
	Enabled bool `json:"enabled"`
	Preferred bool `json:"preferred"`
	GPUMode string `json:"gpu_mode"`
	GPUDevices []string `json:"gpu_devices,omitempty"`
	TensorSplit string `json:"tensor_split,omitempty"`
}

type CreateModelInput struct {
	ModelID string `json:"model_id"`
	DisplayName string `json:"display_name"`
	ArtifactID string `json:"artifact_id"`
	AutoloadEnabled *bool `json:"autoload_enabled,omitempty"`
	AlwaysOn bool `json:"always_on"`
	IdleTimeoutSeconds *int64 `json:"idle_timeout_seconds,omitempty"`
	StartupTimeoutSeconds *int64 `json:"startup_timeout_seconds,omitempty"`
	Priority Priority `json:"priority"`
	RoutingPolicy string `json:"routing_policy"`
	Options map[string]string `json:"options,omitempty"`
}

type UpdateModelInput struct {
	DisplayName *string `json:"display_name,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
	AutoloadEnabled *bool `json:"autoload_enabled,omitempty"`
	AlwaysOn *bool `json:"always_on,omitempty"`
	IdleTimeoutSeconds *int64 `json:"idle_timeout_seconds,omitempty"`
	ClearIdleTimeout bool `json:"clear_idle_timeout,omitempty"`
	StartupTimeoutSeconds *int64 `json:"startup_timeout_seconds,omitempty"`
	ClearStartupTimeout bool `json:"clear_startup_timeout,omitempty"`
	Priority *Priority `json:"priority,omitempty"`
	RoutingPolicy *string `json:"routing_policy,omitempty"`
}

type CreateInstanceInput struct {
	Name string `json:"name"`
	Enabled *bool `json:"enabled,omitempty"`
	Preferred bool `json:"preferred"`
	GPUMode string `json:"gpu_mode"`
	GPUDevices []string `json:"gpu_devices,omitempty"`
	TensorSplit string `json:"tensor_split,omitempty"`
}

type Service struct { db *sql.DB; modelsDir string }
func New(db *sql.DB, modelsDir string)*Service{return &Service{db:db,modelsDir:modelsDir}}

func(s *Service)RegisterLocalArtifact(ctx context.Context,path,displayName string)(Artifact,error){
	cleanPath,rel,err:=s.validateArtifactPath(path);if err!=nil{return Artifact{},err}
	info,err:=os.Stat(cleanPath);if err!=nil{return Artifact{},fmt.Errorf("stat artifact: %w",err)};if info.IsDir(){return Artifact{},errors.New("artifact path must be a file")};if !strings.HasSuffix(strings.ToLower(info.Name()),".gguf"){return Artifact{},errors.New("artifact must be a .gguf file")}
	if displayName==""{displayName=filepath.Base(cleanPath)};id:=newID();quant:=quantizationFromName(info.Name())
	_,err=s.db.ExecContext(ctx,`INSERT INTO artifacts(id,display_name,local_path,total_bytes,quantization,provider,completed) VALUES(?,?,?,?,?,'local',1)`,id,displayName,rel,info.Size(),quant);if err!=nil{return Artifact{},err}
	return Artifact{ID:id,DisplayName:displayName,LocalPath:rel,TotalBytes:info.Size(),Quantization:quant,Provider:"local",Completed:true},nil
}
func(s *Service)ListArtifacts(ctx context.Context)([]Artifact,error){rows,err:=s.db.QueryContext(ctx,`SELECT id,display_name,local_path,total_bytes,COALESCE(quantization,''),provider,COALESCE(source,''),COALESCE(revision,''),completed,created_at FROM artifacts ORDER BY created_at DESC`);if err!=nil{return nil,err};defer rows.Close();var out []Artifact;for rows.Next(){var a Artifact;var completed int;if err:=rows.Scan(&a.ID,&a.DisplayName,&a.LocalPath,&a.TotalBytes,&a.Quantization,&a.Provider,&a.Source,&a.Revision,&completed,&a.CreatedAt);err!=nil{return nil,err};a.Completed=completed!=0;out=append(out,a)};return out,rows.Err()}

func(s *Service)Create(ctx context.Context,in CreateModelInput)(Model,error){
	in.ModelID=strings.TrimSpace(in.ModelID);if err:=validatePublicID(in.ModelID);err!=nil{return Model{},err};if in.ArtifactID==""{return Model{},errors.New("artifact_id is required")};if in.Priority==""{in.Priority="normal"};if err:=validatePriority(in.Priority);err!=nil{return Model{},err};if in.RoutingPolicy==""{in.RoutingPolicy="least_active"};if err:=validateRouting(in.RoutingPolicy);err!=nil{return Model{},err};autoload:=true;if in.AutoloadEnabled!=nil{autoload=*in.AutoloadEnabled}
	tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return Model{},err};defer tx.Rollback();var completed int;if err:=tx.QueryRowContext(ctx,"SELECT completed FROM artifacts WHERE id=?",in.ArtifactID).Scan(&completed);err!=nil||completed==0{return Model{},errors.New("artifact does not exist or is incomplete")}
	id:=newID();_,err=tx.ExecContext(ctx,`INSERT INTO models(id,model_id,display_name,artifact_id,autoload_enabled,always_on,idle_timeout_seconds,startup_timeout_seconds,priority,routing_policy) VALUES(?,?,?,?,?,?,?,?,?,?)`,id,in.ModelID,in.DisplayName,in.ArtifactID,boolInt(autoload),boolInt(in.AlwaysOn),nullableInt(in.IdleTimeoutSeconds),nullableInt(in.StartupTimeoutSeconds),in.Priority,in.RoutingPolicy);if err!=nil{return Model{},err};for key,value:=range in.Options{if _,err:=tx.ExecContext(ctx,"INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)",id,key,value);err!=nil{return Model{},err}};if _,err:=tx.ExecContext(ctx,`INSERT INTO instances(id,model_id,name) VALUES(?,?,?)`,newID(),id,"default");err!=nil{return Model{},err};if err:=tx.Commit();err!=nil{return Model{},err};return s.GetByID(ctx,id)
}
func(s *Service)Update(ctx context.Context,id string,in UpdateModelInput)(Model,error){
	current,err:=s.GetByID(ctx,id);if err!=nil{return Model{},err};if in.DisplayName!=nil{current.DisplayName=*in.DisplayName};if in.Enabled!=nil{current.Enabled=*in.Enabled};if in.AutoloadEnabled!=nil{current.AutoloadEnabled=*in.AutoloadEnabled};if in.AlwaysOn!=nil{current.AlwaysOn=*in.AlwaysOn};if in.Priority!=nil{if err:=validatePriority(*in.Priority);err!=nil{return Model{},err};current.Priority=*in.Priority};if in.RoutingPolicy!=nil{if err:=validateRouting(*in.RoutingPolicy);err!=nil{return Model{},err};current.RoutingPolicy=*in.RoutingPolicy};if in.ClearIdleTimeout{current.IdleTimeoutSeconds=nil}else if in.IdleTimeoutSeconds!=nil{v:=*in.IdleTimeoutSeconds;current.IdleTimeoutSeconds=&v};if in.ClearStartupTimeout{current.StartupTimeoutSeconds=nil}else if in.StartupTimeoutSeconds!=nil{v:=*in.StartupTimeoutSeconds;current.StartupTimeoutSeconds=&v}
	_,err=s.db.ExecContext(ctx,`UPDATE models SET display_name=?,enabled=?,autoload_enabled=?,always_on=?,idle_timeout_seconds=?,startup_timeout_seconds=?,priority=?,routing_policy=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,current.DisplayName,boolInt(current.Enabled),boolInt(current.AutoloadEnabled),boolInt(current.AlwaysOn),nullableInt(current.IdleTimeoutSeconds),nullableInt(current.StartupTimeoutSeconds),current.Priority,current.RoutingPolicy,id);if err!=nil{return Model{},err};return s.GetByID(ctx,id)
}
func(s *Service)List(ctx context.Context)([]Model,error){rows,err:=s.db.QueryContext(ctx,modelSelect+" ORDER BY m.created_at DESC");if err!=nil{return nil,err};defer rows.Close();var out []Model;for rows.Next(){m,err:=scanModel(rows);if err!=nil{return nil,err};out=append(out,m)};return out,rows.Err()}
func(s *Service)GetByID(ctx context.Context,id string)(Model,error){return scanModel(s.db.QueryRowContext(ctx,modelSelect+" WHERE m.id=?",id))}
func(s *Service)GetByPublicID(ctx context.Context,id string)(Model,error){return scanModel(s.db.QueryRowContext(ctx,modelSelect+" WHERE m.model_id=?",id))}
func(s *Service)Delete(ctx context.Context,id string)error{_,err:=s.db.ExecContext(ctx,"DELETE FROM models WHERE id=?",id);return err}

func(s *Service)Options(ctx context.Context,modelID string)(map[string]string,error){rows,err:=s.db.QueryContext(ctx,"SELECT option_key,option_value FROM model_options WHERE model_id=?",modelID);if err!=nil{return nil,err};defer rows.Close();out:=map[string]string{};for rows.Next(){var k,v string;if err:=rows.Scan(&k,&v);err!=nil{return nil,err};out[k]=v};return out,rows.Err()}
func(s *Service)ReplaceOptions(ctx context.Context,modelID string,options map[string]string)error{tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return err};defer tx.Rollback();if _,err:=tx.ExecContext(ctx,"DELETE FROM model_options WHERE model_id=?",modelID);err!=nil{return err};for key,value:=range options{if strings.TrimSpace(key)==""{return errors.New("empty option key")};if len(value)>64*1024{return errors.New("option value too large")};if _,err:=tx.ExecContext(ctx,"INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)",modelID,strings.TrimLeft(key,"-"),value);err!=nil{return err}};if _,err:=tx.ExecContext(ctx,"UPDATE models SET updated_at=CURRENT_TIMESTAMP WHERE id=?",modelID);err!=nil{return err};return tx.Commit()}

func(s *Service)Instances(ctx context.Context,modelID string)([]Instance,error){rows,err:=s.db.QueryContext(ctx,`SELECT id,model_id,name,enabled,preferred,gpu_mode,COALESCE(gpu_devices,''),COALESCE(tensor_split,'') FROM instances WHERE model_id=? ORDER BY created_at`,modelID);if err!=nil{return nil,err};defer rows.Close();var out []Instance;for rows.Next(){item,err:=scanInstance(rows);if err!=nil{return nil,err};out=append(out,item)};return out,rows.Err()}
func(s *Service)GetInstance(ctx context.Context,id string)(Instance,error){return scanInstance(s.db.QueryRowContext(ctx,`SELECT id,model_id,name,enabled,preferred,gpu_mode,COALESCE(gpu_devices,''),COALESCE(tensor_split,'') FROM instances WHERE id=?`,id))}
func(s *Service)CreateInstance(ctx context.Context,modelID string,in CreateInstanceInput)(Instance,error){if _,err:=s.GetByID(ctx,modelID);err!=nil{return Instance{},err};if strings.TrimSpace(in.Name)==""{in.Name="instance"};if in.GPUMode==""{in.GPUMode="auto"};if in.GPUMode!="auto"&&in.GPUMode!="manual"{return Instance{},errors.New("gpu_mode must be auto or manual")};enabled:=true;if in.Enabled!=nil{enabled=*in.Enabled};if in.Preferred{_,_=s.db.ExecContext(ctx,"UPDATE instances SET preferred=0 WHERE model_id=?",modelID)};id:=newID();_,err:=s.db.ExecContext(ctx,`INSERT INTO instances(id,model_id,name,enabled,preferred,gpu_mode,gpu_devices,tensor_split) VALUES(?,?,?,?,?,?,?,?)`,id,modelID,in.Name,boolInt(enabled),boolInt(in.Preferred),in.GPUMode,strings.Join(in.GPUDevices,","),in.TensorSplit);if err!=nil{return Instance{},err};return s.GetInstance(ctx,id)}
func(s *Service)UpdateInstance(ctx context.Context,id string,in CreateInstanceInput)(Instance,error){current,err:=s.GetInstance(ctx,id);if err!=nil{return Instance{},err};if in.Name!=""{current.Name=in.Name};if in.Enabled!=nil{current.Enabled=*in.Enabled};if in.GPUMode!=""{if in.GPUMode!="auto"&&in.GPUMode!="manual"{return Instance{},errors.New("gpu_mode must be auto or manual")};current.GPUMode=in.GPUMode};current.Preferred=in.Preferred;current.GPUDevices=in.GPUDevices;current.TensorSplit=in.TensorSplit;if current.Preferred{_,_=s.db.ExecContext(ctx,"UPDATE instances SET preferred=0 WHERE model_id=? AND id<>?",current.ModelID,id)};_,err=s.db.ExecContext(ctx,`UPDATE instances SET name=?,enabled=?,preferred=?,gpu_mode=?,gpu_devices=?,tensor_split=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,current.Name,boolInt(current.Enabled),boolInt(current.Preferred),current.GPUMode,strings.Join(current.GPUDevices,","),current.TensorSplit,id);if err!=nil{return Instance{},err};return s.GetInstance(ctx,id)}
func(s *Service)DeleteInstance(ctx context.Context,id string)error{instance,err:=s.GetInstance(ctx,id);if err!=nil{return err};var count int;if err:=s.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM instances WHERE model_id=?",instance.ModelID).Scan(&count);err!=nil{return err};if count<=1{return errors.New("cannot delete the final instance definition")};_,err=s.db.ExecContext(ctx,"DELETE FROM instances WHERE id=?",id);return err}

func(s *Service)ArtifactAbsolutePath(m Model)(string,error){root,err:=filepath.Abs(s.modelsDir);if err!=nil{return "",err};path,_,err:=s.validateArtifactPath(filepath.Join(root,m.ArtifactPath));return path,err}
func(s *Service)validateArtifactPath(path string)(string,string,error){if strings.TrimSpace(path)==""{return "","",errors.New("path is required")};root,err:=filepath.Abs(s.modelsDir);if err!=nil{return "","",err};candidate:=path;if !filepath.IsAbs(candidate){candidate=filepath.Join(root,candidate)};clean,err:=filepath.Abs(candidate);if err!=nil{return "","",err};rel,err:=filepath.Rel(root,clean);if err!=nil||rel==".."||strings.HasPrefix(rel,".."+string(filepath.Separator)){return "","",errors.New("artifact must be inside configured models directory")};return clean,rel,nil}

const modelSelect=`SELECT m.id,m.model_id,COALESCE(m.display_name,''),m.artifact_id,a.local_path,m.enabled,m.autoload_enabled,m.always_on,m.idle_timeout_seconds,m.startup_timeout_seconds,m.priority,m.routing_policy,m.created_at,m.updated_at FROM models m JOIN artifacts a ON a.id=m.artifact_id`
type scanner interface{Scan(dest ...any)error}
func scanModel(row scanner)(Model,error){var m Model;var enabled,autoload,always int;var idle,startup sql.NullInt64;if err:=row.Scan(&m.ID,&m.ModelID,&m.DisplayName,&m.ArtifactID,&m.ArtifactPath,&enabled,&autoload,&always,&idle,&startup,&m.Priority,&m.RoutingPolicy,&m.CreatedAt,&m.UpdatedAt);err!=nil{return Model{},err};m.Enabled=enabled!=0;m.AutoloadEnabled=autoload!=0;m.AlwaysOn=always!=0;if idle.Valid{v:=idle.Int64;m.IdleTimeoutSeconds=&v};if startup.Valid{v:=startup.Int64;m.StartupTimeoutSeconds=&v};return m,nil}
func scanInstance(row scanner)(Instance,error){var i Instance;var enabled,preferred int;var devices string;if err:=row.Scan(&i.ID,&i.ModelID,&i.Name,&enabled,&preferred,&i.GPUMode,&devices,&i.TensorSplit);err!=nil{return Instance{},err};i.Enabled=enabled!=0;i.Preferred=preferred!=0;if devices!=""{i.GPUDevices=strings.Split(devices,",")};return i,nil}
func validatePublicID(id string)error{if id==""||strings.ContainsAny(id,"\\/\t\r\n "){return errors.New("model_id must be non-empty and may not contain whitespace or path separators")};return nil}
func validatePriority(p Priority)error{if p!="low"&&p!="normal"&&p!="high"{return errors.New("priority must be low, normal, or high")};return nil}
func validateRouting(p string)error{switch p{case "least_active","round_robin","preferred","fixed","lowest_load":return nil;default:return errors.New("unsupported routing policy")}}
func nullableInt(v *int64)any{if v==nil{return nil};return *v}
func boolInt(v bool)int{if v{return 1};return 0}
func newID()string{b:=make([]byte,16);if _,err:=rand.Read(b);err!=nil{return fmt.Sprintf("%d",time.Now().UnixNano())};return hex.EncodeToString(b)}
func quantizationFromName(name string)string{upper:=strings.ToUpper(name);for _,q:=range []string{"IQ1_S","IQ1_M","IQ2_XXS","IQ2_XS","IQ2_S","IQ2_M","IQ3_XXS","IQ3_XS","IQ3_S","IQ3_M","IQ4_XS","IQ4_NL","Q2_K","Q3_K_S","Q3_K_M","Q3_K_L","Q4_0","Q4_1","Q4_K_S","Q4_K_M","Q5_0","Q5_1","Q5_K_S","Q5_K_M","Q6_K","Q8_0","F16","BF16","F32"}{if strings.Contains(upper,q){return q}};return ""}
