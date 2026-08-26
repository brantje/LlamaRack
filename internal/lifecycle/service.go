package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/internal/models"
	"github.com/brantje/llamacpp-manager/internal/scheduler"
	"github.com/brantje/llamacpp-manager/internal/supervisor"
)

type Service struct {
	models *models.Service
	sup *supervisor.Supervisor
	scheduler *scheduler.Service
	mu sync.Mutex
	loads map[string]*loadCall
}

type loadCall struct { done chan struct{}; endpoint string; err error }

func New(modelService *models.Service, sup *supervisor.Supervisor, schedulerService *scheduler.Service) *Service {
	return &Service{models:modelService,sup:sup,scheduler:schedulerService,loads:map[string]*loadCall{}}
}

func(s *Service)EnsureReady(ctx context.Context,publicModelID string)(string,error){model,err:=s.models.GetByPublicID(ctx,publicModelID);if err!=nil{return "",err};if !model.Enabled{return "",errors.New("model is disabled")};if endpoint,ok:=s.readyEndpoint(ctx,model);ok{return endpoint,nil};if !model.AutoloadEnabled{return "",errors.New("model is unloaded and autoload is disabled")};return s.startSingleFlight(ctx,model)}
func(s *Service)StartModel(ctx context.Context,modelID string)(string,error){model,err:=s.models.GetByID(ctx,modelID);if err!=nil{return "",err};if endpoint,ok:=s.readyEndpoint(ctx,model);ok{return endpoint,nil};return s.startSingleFlight(ctx,model)}
func(s *Service)StopModel(ctx context.Context,modelID string)error{instances,err:=s.models.Instances(ctx,modelID);if err!=nil{return err};for _,instance:=range instances{if err:=s.sup.Stop(ctx,instance.ID);err!=nil&&!errors.Is(err,context.Canceled){return err}};return nil}
func(s *Service)RuntimeForModel(ctx context.Context,modelID string)([]supervisor.Runtime,error){instances,err:=s.models.Instances(ctx,modelID);if err!=nil{return nil,err};out:=make([]supervisor.Runtime,0,len(instances));for _,instance:=range instances{out=append(out,s.sup.Status(instance.ID))};return out,nil}
func(s *Service)Logs(instanceID string)[]string{return s.sup.Logs(instanceID)}
func(s *Service)ReconcileAlwaysOn(ctx context.Context){modelsList,err:=s.models.List(ctx);if err!=nil{return};for _,model:=range modelsList{if !model.Enabled||!model.AlwaysOn{continue};if _,ok:=s.readyEndpoint(ctx,model);ok{continue};go func(id string){_,_=s.StartModel(context.Background(),id)}(model.ID)}}
func(s *Service)RunReconciler(ctx context.Context,interval time.Duration){s.ReconcileAlwaysOn(ctx);ticker:=time.NewTicker(interval);defer ticker.Stop();for{select{case<-ctx.Done():return;case<-ticker.C:s.ReconcileAlwaysOn(ctx)}}}

func(s *Service)startSingleFlight(ctx context.Context,model models.Model)(string,error){s.mu.Lock();if existing:=s.loads[model.ID];existing!=nil{s.mu.Unlock();select{case<-ctx.Done():return "",ctx.Err();case<-existing.done:return existing.endpoint,existing.err}};call:=&loadCall{done:make(chan struct{})};s.loads[model.ID]=call;s.mu.Unlock();endpoint,err:=s.startOne(ctx,model);call.endpoint,call.err=endpoint,err;close(call.done);s.mu.Lock();delete(s.loads,model.ID);s.mu.Unlock();return endpoint,err}

func(s *Service)startOne(ctx context.Context,model models.Model)(string,error){
	instances,err:=s.models.Instances(ctx,model.ID);if err!=nil{return "",err};if len(instances)==0{return "",errors.New("model has no instance definition")};var selected *models.Instance;for i:=range instances{if instances[i].Enabled{selected=&instances[i];break}};if selected==nil{return "",errors.New("model has no enabled instance")}
	plan,err:=s.scheduler.PlanStart(ctx,model);if err!=nil{return "",err};defer s.scheduler.Release(model.ID)
	for _,victimID:=range plan.EvictInstanceIDs{stopCtx,cancel:=context.WithTimeout(ctx,30*time.Second);err:=s.sup.Stop(stopCtx,victimID);cancel();if err!=nil{return "",fmt.Errorf("evict instance %s: %w",victimID,err)}}
	path,err:=s.models.ArtifactAbsolutePath(model);if err!=nil{return "",err};options,err:=s.models.Options(ctx,model.ID);if err!=nil{return "",err};extraArgs:=optionArgs(options);if selected.GPUMode=="manual"&&len(selected.GPUDevices)>0{extraArgs=append(extraArgs,"--device",strings.Join(selected.GPUDevices,","))};if selected.TensorSplit!=""{extraArgs=append(extraArgs,"--tensor-split",selected.TensorSplit)}
	rt,err:=s.sup.Start(ctx,selected.ID,model.ID,path,extraArgs);if err!=nil{return "",fmt.Errorf("start %s: %w",model.ModelID,err)};endpoint,ok:=s.sup.Endpoint(selected.ID);if !ok{return "",fmt.Errorf("worker %s reached unexpected state %s",selected.ID,rt.State)};return endpoint,nil
}
func(s *Service)readyEndpoint(ctx context.Context,model models.Model)(string,bool){instances,err:=s.models.Instances(ctx,model.ID);if err!=nil{return "",false};for _,instance:=range instances{if endpoint,ok:=s.sup.Endpoint(instance.ID);ok{return endpoint,true}};return "",false}
func optionArgs(options map[string]string)[]string{keys:=make([]string,0,len(options));for key:=range options{keys=append(keys,key)};sort.Strings(keys);args:=make([]string,0,len(keys)*2);for _,key:=range keys{value:=strings.TrimSpace(options[key]);flag:="--"+strings.TrimLeft(key,"-");switch strings.ToLower(value){case "true":args=append(args,flag);case "false","":[0]default:args=append(args,flag,value)}};return args}
