package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/brantje/llamacpp-manager/internal/hardware"
	"github.com/brantje/llamacpp-manager/internal/models"
	"github.com/brantje/llamacpp-manager/internal/router"
	"github.com/brantje/llamacpp-manager/internal/supervisor"
)

type Plan struct {
	EstimatedRAM uint64 `json:"estimated_ram"`
	AvailableRAM uint64 `json:"available_ram"`
	EvictInstanceIDs []string `json:"evict_instance_ids,omitempty"`
	Reason string `json:"reason"`
}

type Service struct {
	mu sync.Mutex
	hardware *hardware.Service
	models *models.Service
	sup *supervisor.Supervisor
	router *router.Service
	reserved map[string]uint64
}

func New(hw *hardware.Service, modelService *models.Service, sup *supervisor.Supervisor, requestRouter *router.Service)*Service{
	return &Service{hardware:hw,models:modelService,sup:sup,router:requestRouter,reserved:map[string]uint64{}}
}

func(s *Service)PlanStart(ctx context.Context,target models.Model)(Plan,error){
	path,err:=s.models.ArtifactAbsolutePath(target);if err!=nil{return Plan{},err};stat,err:=os.Stat(path);if err!=nil{return Plan{},fmt.Errorf("artifact: %w",err)}
	need:=estimateRAM(uint64(stat.Size()));snap:=s.hardware.Snapshot(ctx);if snap.Memory.TotalBytes==0{return Plan{},errors.New("system memory telemetry unavailable")}
	s.mu.Lock();defer s.mu.Unlock();var reserved uint64;for id,value:=range s.reserved{if id!=target.ID{reserved+=value}}
	available:=snap.Memory.AvailableBytes;if reserved>=available{available=0}else{available-=reserved};usable:=available*90/100
	plan:=Plan{EstimatedRAM:need,AvailableRAM:usable}
	if need<=usable{s.reserved[target.ID]=need;plan.Reason="resources available";return plan,nil}
	deficit:=need-usable;victims:=s.evictionCandidates(ctx,target.ID);var freed uint64
	for _,candidate:=range victims{plan.EvictInstanceIDs=append(plan.EvictInstanceIDs,candidate.instanceID);freed+=candidate.estimatedRAM;if freed>=deficit{break}}
	if freed<deficit{return plan,fmt.Errorf("insufficient resources: need approximately %d MiB, only %d MiB schedulable and %d MiB evictable",need/(1024*1024),usable/(1024*1024),freed/(1024*1024))}
	s.reserved[target.ID]=need;plan.Reason="resources available after eviction";return plan,nil
}
func(s *Service)Release(modelID string){s.mu.Lock();delete(s.reserved,modelID);s.mu.Unlock()}

type victim struct{instanceID string;priority int;started int64;estimatedRAM uint64}
func(s *Service)evictionCandidates(ctx context.Context,targetModelID string)[]victim{runtimes:=s.sup.All();readyByModel:=map[string]int{};for _,rt:=range runtimes{if rt.State==supervisor.Ready{readyByModel[rt.ModelID]++}};var result []victim;for _,rt:=range runtimes{if rt.State!=supervisor.Ready||rt.ModelID==targetModelID||s.router.Active(rt.InstanceID)>0{continue};model,err:=s.models.GetByID(ctx,rt.ModelID);if err!=nil{continue};if model.AlwaysOn&&readyByModel[model.ID]<=1{continue};path,err:=s.models.ArtifactAbsolutePath(model);if err!=nil{continue};stat,err:=os.Stat(path);if err!=nil{continue};result=append(result,victim{instanceID:rt.InstanceID,priority:priorityValue(model.Priority),started:rt.StartedAt.Unix(),estimatedRAM:estimateRAM(uint64(stat.Size()))})};sort.Slice(result,func(i,j int)bool{if result[i].priority!=result[j].priority{return result[i].priority<result[j].priority};return result[i].started<result[j].started});return result}
func priorityValue(p models.Priority)int{switch p{case "low":return 0;case "high":return 2;default:return 1}}
func estimateRAM(fileBytes uint64)uint64{return fileBytes*120/100+512*1024*1024}
