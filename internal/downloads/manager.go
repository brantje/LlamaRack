package downloads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/internal/models"
	"github.com/brantje/llamacpp-manager/internal/providers"
)

type State string
const(
	Queued State="QUEUED";Resolving State="RESOLVING";Downloading State="DOWNLOADING";Verifying State="VERIFYING";Completed State="COMPLETED";Failed State="FAILED";Cancelled State="CANCELLED"
)

type Job struct{
	ID string `json:"id"`
	Provider string `json:"provider"`
	Source string `json:"source"`
	Filename string `json:"filename"`
	State State `json:"state"`
	TotalBytes int64 `json:"total_bytes"`
	CompletedBytes int64 `json:"completed_bytes"`
	BytesPerSecond float64 `json:"bytes_per_second"`
	Error string `json:"error,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Manager struct{
	mu sync.RWMutex
	modelsDir string
	models *models.Service
	hf *providers.HuggingFace
	client *http.Client
	jobs map[string]*Job
	cancels map[string]context.CancelFunc
	allowPrivateNetworks bool
}

func New(modelsDir string, modelService *models.Service, hf *providers.HuggingFace) *Manager {
	return &Manager{modelsDir:modelsDir,models:modelService,hf:hf,jobs:map[string]*Job{},cancels:map[string]context.CancelFunc{},client:&http.Client{Timeout:0,CheckRedirect:func(req *http.Request,via []*http.Request)error{if len(via)>=8{return errors.New("too many redirects")};return nil}}}
}

func(m *Manager)List()[]Job{m.mu.RLock();defer m.mu.RUnlock();out:=make([]Job,0,len(m.jobs));for _,j:=range m.jobs{out=append(out,*j)};return out}
func(m *Manager)Cancel(id string)error{m.mu.Lock();cancel:=m.cancels[id];j:=m.jobs[id];m.mu.Unlock();if j==nil{return errors.New("download not found")};if cancel!=nil{cancel()};return nil}

func(m *Manager)StartHuggingFace(repo,file,revision string)(Job,error){target,err:=m.hf.DownloadURL(repo,file,revision);if err!=nil{return Job{},err};name:=filepath.Base(file);return m.start("huggingface",repo+"/"+file,target,name,true)}
func(m *Manager)StartURL(rawURL string)(Job,error){u,err:=url.Parse(rawURL);if err!=nil||u.Scheme!="https"&&u.Scheme!="http"{return Job{},errors.New("only http/https URLs are supported")};if u.User!=nil{return Job{},errors.New("embedded URL credentials are not supported")};if !m.allowPrivateNetworks{if err:=rejectPrivateHost(u.Hostname());err!=nil{return Job{},err}};name:=filepath.Base(u.Path);if name==""||name=="."||name=="/"{return Job{},errors.New("URL must contain a filename")};return m.start("url",rawURL,rawURL,name,false)}

func(m *Manager)start(provider,source,target,filename string,hfAuth bool)(Job,error){filename=safeFilename(filename);if !strings.HasSuffix(strings.ToLower(filename),".gguf"){return Job{},errors.New("only .gguf downloads are supported")};id:=newID();ctx,cancel:=context.WithCancel(context.Background());job:=&Job{ID:id,Provider:provider,Source:source,Filename:filename,State:Queued,CreatedAt:time.Now().UTC()};m.mu.Lock();m.jobs[id]=job;m.cancels[id]=cancel;m.mu.Unlock();go m.run(ctx,job,target,hfAuth);return *job,nil}

func(m *Manager)run(ctx context.Context,job *Job,target string,hfAuth bool){now:=time.Now().UTC();m.update(job.ID,func(j *Job){j.State=Resolving;j.StartedAt=&now});finalPath:=filepath.Join(m.modelsDir,job.Filename);partPath:=finalPath+".part"
	if err:=os.MkdirAll(m.modelsDir,0o750);err!=nil{m.fail(job.ID,err);return}
	var offset int64;if info,err:=os.Stat(partPath);err==nil{offset=info.Size()}
	req,err:=http.NewRequestWithContext(ctx,http.MethodGet,target,nil);if err!=nil{m.fail(job.ID,err);return};if hfAuth{m.hf.ApplyAuth(req)};if offset>0{req.Header.Set("Range",fmt.Sprintf("bytes=%d-",offset))}
	if !m.allowPrivateNetworks { if u,err:=url.Parse(target);err==nil&&job.Provider=="url"{if err:=rejectPrivateHost(u.Hostname());err!=nil{m.fail(job.ID,err);return}} }
	resp,err:=m.client.Do(req);if err!=nil{if errors.Is(err,context.Canceled){m.cancelled(job.ID)}else{m.fail(job.ID,err)};return};defer resp.Body.Close()
	if resp.StatusCode!=http.StatusOK&&resp.StatusCode!=http.StatusPartialContent{m.fail(job.ID,fmt.Errorf("download returned %s",resp.Status));return};if offset>0&&resp.StatusCode==http.StatusOK{offset=0;_ = os.Remove(partPath)}
	total:=resp.ContentLength;if resp.StatusCode==http.StatusPartialContent&&total>=0{total+=offset};m.update(job.ID,func(j *Job){j.State=Downloading;j.TotalBytes=total;j.CompletedBytes=offset})
	flags:=os.O_CREATE|os.O_WRONLY;if offset>0{flags|=os.O_APPEND}else{flags|=os.O_TRUNC};file,err:=os.OpenFile(partPath,flags,0o640);if err!=nil{m.fail(job.ID,err);return}
	buf:=make([]byte,1024*1024);written:=offset;started:=time.Now();last:=started;lastBytes:=written
	for{n,readErr:=resp.Body.Read(buf);if n>0{if _,err:=file.Write(buf[:n]);err!=nil{_ = file.Close();m.fail(job.ID,err);return};written+=int64(n);now:=time.Now();if now.Sub(last)>=500*time.Millisecond{delta:=written-lastBytes;seconds:=now.Sub(last).Seconds();m.update(job.ID,func(j *Job){j.CompletedBytes=written;j.BytesPerSecond=float64(delta)/seconds});last=now;lastBytes=written}}
		if readErr==io.EOF{break};if readErr!=nil{_ = file.Close();if errors.Is(readErr,context.Canceled)||errors.Is(ctx.Err(),context.Canceled){m.cancelled(job.ID)}else{m.fail(job.ID,readErr)};return};select{case<-ctx.Done():_ = file.Close();m.cancelled(job.ID);return;default:}}
	if err:=file.Sync();err!=nil{_ = file.Close();m.fail(job.ID,err);return};_ = file.Close();m.update(job.ID,func(j *Job){j.State=Verifying;j.CompletedBytes=written})
	if total>0&&written!=total{m.fail(job.ID,fmt.Errorf("download size mismatch: got %d expected %d",written,total));return};if err:=os.Rename(partPath,finalPath);err!=nil{m.fail(job.ID,err);return}
	artifact,err:=m.models.RegisterLocalArtifact(context.Background(),finalPath,job.Filename);if err!=nil{m.fail(job.ID,err);return};done:=time.Now().UTC();elapsed:=time.Since(started).Seconds();m.update(job.ID,func(j *Job){j.State=Completed;j.CompletedBytes=written;j.TotalBytes=written;j.ArtifactID=artifact.ID;j.CompletedAt=&done;if elapsed>0{j.BytesPerSecond=float64(written-offset)/elapsed}});m.mu.Lock();delete(m.cancels,job.ID);m.mu.Unlock()}

func(m *Manager)update(id string,fn func(*Job)){m.mu.Lock();defer m.mu.Unlock();if j:=m.jobs[id];j!=nil{fn(j)}}
func(m *Manager)fail(id string,err error){m.update(id,func(j *Job){j.State=Failed;j.Error=err.Error()});m.mu.Lock();delete(m.cancels,id);m.mu.Unlock()}
func(m *Manager)cancelled(id string){m.update(id,func(j *Job){j.State=Cancelled;j.Error=""});m.mu.Lock();delete(m.cancels,id);m.mu.Unlock()}
func safeFilename(name string)string{name=filepath.Base(strings.TrimSpace(name));name=strings.ReplaceAll(name,"\x00","");return name}
func newID()string{b:=make([]byte,12);_,_=rand.Read(b);return hex.EncodeToString(b)}
func rejectPrivateHost(host string)error{ips,err:=net.LookupIP(host);if err!=nil{return fmt.Errorf("resolve download host: %w",err)};for _,ip:=range ips{if ip.IsLoopback()||ip.IsPrivate()||ip.IsLinkLocalUnicast()||ip.IsLinkLocalMulticast(){return errors.New("private-network direct downloads are disabled")}};return nil}
