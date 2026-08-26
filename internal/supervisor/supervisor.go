package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type State string

const (
	Unloaded State = "UNLOADED"
	Starting State = "STARTING"
	Loading  State = "LOADING"
	Ready    State = "READY"
	Stopping State = "STOPPING"
	Failed   State = "FAILED"
)

type Runtime struct {
	InstanceID string    `json:"instance_id"`
	ModelID    string    `json:"model_id"`
	State      State     `json:"state"`
	PID        int       `json:"pid,omitempty"`
	Port       int       `json:"port,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	ReadyAt    time.Time `json:"ready_at,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

type worker struct {
	runtime Runtime
	cmd     *exec.Cmd
	logs    *ringLog
	done    chan struct{}
}

type Supervisor struct {
	mu             sync.RWMutex
	binary         string
	host           string
	portStart      int
	startupTimeout time.Duration
	workers        map[string]*worker
}

func New(binary, host string, portStart int, startupTimeout time.Duration) *Supervisor {
	return &Supervisor{binary: binary, host: host, portStart: portStart, startupTimeout: startupTimeout, workers: map[string]*worker{}}
}

func (s *Supervisor) Start(ctx context.Context, instanceID, modelID, modelPath string, extraArgs []string) (Runtime, error) {
	s.mu.Lock()
	if existing := s.workers[instanceID]; existing != nil && existing.runtime.State != Failed && existing.runtime.State != Unloaded {
		rt := existing.runtime
		s.mu.Unlock()
		return rt, nil
	}
	port, err := s.allocatePortLocked()
	if err != nil {
		s.mu.Unlock()
		return Runtime{}, err
	}
	workerArgs := []string{"--model", modelPath, "--host", s.host, "--port", fmt.Sprint(port)}
	workerArgs = append(workerArgs, extraArgs...)
	cmd := exec.Command(s.binary, workerArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil { s.mu.Unlock(); return Runtime{}, err }
	stderr, err := cmd.StderrPipe()
	if err != nil { s.mu.Unlock(); return Runtime{}, err }
	w := &worker{runtime: Runtime{InstanceID: instanceID, ModelID: modelID, State: Starting, Port: port, StartedAt: time.Now().UTC()}, logs: newRingLog(2000), done: make(chan struct{})}
	s.workers[instanceID] = w
	if err := cmd.Start(); err != nil {
		w.runtime.State = Failed; w.runtime.LastError = err.Error(); s.mu.Unlock(); return w.runtime, err
	}
	w.cmd = cmd
	w.runtime.PID = cmd.Process.Pid
	s.mu.Unlock()

	go copyLogs(w.logs, "stdout", stdout)
	go copyLogs(w.logs, "stderr", stderr)
	go s.wait(w)

	s.setState(instanceID, Loading, "")
	waitCtx, cancel := context.WithTimeout(ctx, s.startupTimeout)
	defer cancel()
	if err := s.waitReady(waitCtx, port); err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = s.Stop(stopCtx, instanceID)
		stopCancel()
		s.setState(instanceID, Failed, err.Error())
		return s.Status(instanceID), err
	}
	s.mu.Lock()
	current := s.workers[instanceID]
	if current == nil {
		s.mu.Unlock()
		return Runtime{InstanceID: instanceID, State: Failed}, errors.New("worker disappeared during startup")
	}
	current.runtime.State = Ready
	current.runtime.ReadyAt = time.Now().UTC()
	rt := current.runtime
	s.mu.Unlock()
	return rt, nil
}

func (s *Supervisor) Stop(ctx context.Context, instanceID string) error {
	s.mu.Lock()
	w := s.workers[instanceID]
	if w == nil || w.cmd == nil || w.cmd.Process == nil || w.runtime.State == Unloaded {
		s.mu.Unlock()
		return nil
	}
	w.runtime.State = Stopping
	process := w.cmd.Process
	done := w.done
	s.mu.Unlock()

	_ = process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = process.Kill()
		return ctx.Err()
	case <-time.After(15 * time.Second):
		_ = process.Kill()
		select { case <-done: case <-time.After(5*time.Second): }
		return nil
	}
}

func (s *Supervisor) Status(instanceID string) Runtime {
	s.mu.RLock(); defer s.mu.RUnlock()
	if w := s.workers[instanceID]; w != nil { return w.runtime }
	return Runtime{InstanceID: instanceID, State: Unloaded}
}

func (s *Supervisor) All() []Runtime {
	s.mu.RLock(); defer s.mu.RUnlock()
	out := make([]Runtime,0,len(s.workers))
	for _,w := range s.workers { out=append(out,w.runtime) }
	return out
}

func (s *Supervisor) Endpoint(instanceID string) (string, bool) {
	rt := s.Status(instanceID)
	if rt.State != Ready { return "", false }
	return fmt.Sprintf("http://%s:%d",s.host,rt.Port), true
}

func (s *Supervisor) Logs(instanceID string) []string {
	s.mu.RLock(); w:=s.workers[instanceID]; s.mu.RUnlock()
	if w==nil { return nil }
	return w.logs.lines()
}

func (s *Supervisor) Shutdown(ctx context.Context) {
	s.mu.RLock(); ids:=make([]string,0,len(s.workers)); for id:=range s.workers{ids=append(ids,id)}; s.mu.RUnlock()
	var wg sync.WaitGroup
	for _,id:=range ids { wg.Add(1); go func(id string){defer wg.Done(); _=s.Stop(ctx,id)}(id) }
	done:=make(chan struct{}); go func(){wg.Wait();close(done)}()
	select{case<-done:case<-ctx.Done():}
}

func (s *Supervisor) wait(w *worker) {
	err := w.cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if current:=s.workers[w.runtime.InstanceID]; current!=w { return }
	previous := w.runtime.State
	w.runtime.PID=0
	if previous==Stopping { w.runtime.State=Unloaded; w.runtime.LastError="" } else { w.runtime.State=Failed; if err!=nil {w.runtime.LastError=err.Error()} else {w.runtime.LastError="worker exited unexpectedly"} }
	close(w.done)
}

func (s *Supervisor) waitReady(ctx context.Context, port int) error {
	client:=&http.Client{Timeout:2*time.Second}
	ticker:=time.NewTicker(500*time.Millisecond); defer ticker.Stop()
	url:=fmt.Sprintf("http://%s:%d/health",s.host,port)
	for {
		req,_:=http.NewRequestWithContext(ctx,http.MethodGet,url,nil)
		resp,err:=client.Do(req)
		if err==nil { _=resp.Body.Close(); if resp.StatusCode>=200&&resp.StatusCode<300{return nil} }
		select{case<-ctx.Done():return fmt.Errorf("worker readiness timeout: %w",ctx.Err());case<-ticker.C:}
	}
}

func (s *Supervisor) allocatePortLocked()(int,error){
	for port:=s.portStart;port<s.portStart+2000;port++{
		used:=false;for _,w:=range s.workers{if w.runtime.Port==port&&w.runtime.State!=Unloaded&&w.runtime.State!=Failed{used=true;break}}
		if used{continue}
		ln,err:=net.Listen("tcp",fmt.Sprintf("%s:%d",s.host,port));if err!=nil{continue};_ = ln.Close();return port,nil
	}
	return 0,errors.New("no free worker port available")
}
func(s *Supervisor)setState(id string,state State,msg string){s.mu.Lock();defer s.mu.Unlock();if w:=s.workers[id];w!=nil{w.runtime.State=state;w.runtime.LastError=msg}}

type ringLog struct{mu sync.Mutex;max int;data []string}
func newRingLog(max int)*ringLog{return &ringLog{max:max}}
func(r *ringLog)add(line string){r.mu.Lock();defer r.mu.Unlock();if len(r.data)>=r.max{copy(r.data,r.data[1:]);r.data[len(r.data)-1]=line}else{r.data=append(r.data,line)}}
func(r *ringLog)lines()[]string{r.mu.Lock();defer r.mu.Unlock();out:=make([]string,len(r.data));copy(out,r.data);return out}
func copyLogs(dst *ringLog,source string,reader io.Reader){scanner:=bufio.NewScanner(reader);buf:=make([]byte,64*1024);scanner.Buffer(buf,1024*1024);for scanner.Scan(){dst.add("["+source+"] "+scanner.Text())}}
