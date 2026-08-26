package router

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/brantje/llamacpp-manager/internal/models"
	"github.com/brantje/llamacpp-manager/internal/supervisor"
)

type Reservation struct {
	InstanceID string
	Endpoint   string
	release    func()
	once       sync.Once
}

func (r *Reservation) Release() { if r != nil && r.release != nil { r.once.Do(r.release) } }

type Service struct {
	models *models.Service
	sup    *supervisor.Supervisor
	mu     sync.Mutex
	active map[string]int
	rr     map[string]uint64
}

func New(modelService *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{models:modelService,sup:sup,active:map[string]int{},rr:map[string]uint64{}}
}

type candidate struct { id, endpoint string; preferred bool }

func (s *Service) Reserve(ctx context.Context, model models.Model) (*Reservation,error) {
	instances,err:=s.models.Instances(ctx,model.ID);if err!=nil{return nil,err}
	candidates:=make([]candidate,0,len(instances))
	for _,instance:=range instances{if !instance.Enabled{continue};if endpoint,ok:=s.sup.Endpoint(instance.ID);ok{candidates=append(candidates,candidate{id:instance.ID,endpoint:endpoint,preferred:instance.Preferred})}}
	if len(candidates)==0{return nil,errors.New("no ready model instance")}

	s.mu.Lock()
	selected:=s.selectLocked(model,candidates)
	s.active[selected.id]++
	s.mu.Unlock()

	if _,ok:=s.sup.Endpoint(selected.id);!ok{s.release(selected.id);return s.Reserve(ctx,model)}
	return &Reservation{InstanceID:selected.id,Endpoint:selected.endpoint,release:func(){s.release(selected.id)}},nil
}

func(s *Service)selectLocked(model models.Model,candidates []candidate)candidate{
	switch model.RoutingPolicy{
	case "round_robin":
		index:=int(s.rr[model.ID]%uint64(len(candidates)));s.rr[model.ID]++;return candidates[index]
	case "fixed","preferred":
		for _,c:=range candidates{if c.preferred{return c}}
		return s.leastActiveLocked(candidates)
	case "lowest_load":
		// Until normalized worker/GPU load telemetry is available, active requests are
		// the most reliable comparable load signal and intentionally serve as fallback.
		return s.leastActiveLocked(candidates)
	default:
		return s.leastActiveLocked(candidates)
	}
}
func(s *Service)leastActiveLocked(candidates []candidate)candidate{best:=candidates[0];count:=math.MaxInt;for _,c:=range candidates{if s.active[c.id]<count{best=c;count=s.active[c.id]}};return best}
func(s *Service)release(id string){s.mu.Lock();defer s.mu.Unlock();if s.active[id]>0{s.active[id]--}}
func(s *Service)Active(instanceID string)int{s.mu.Lock();defer s.mu.Unlock();return s.active[instanceID]}
