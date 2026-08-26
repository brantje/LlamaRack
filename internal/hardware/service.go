package hardware

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Memory struct { TotalBytes uint64 `json:"total_bytes"`; AvailableBytes uint64 `json:"available_bytes"` }
type GPU struct { ID string `json:"id"`; Index int `json:"index"`; Vendor string `json:"vendor"`; Name string `json:"name"`; TotalVRAM uint64 `json:"total_vram"`; FreeVRAM uint64 `json:"free_vram"`; Utilization float64 `json:"utilization"` }
type Snapshot struct { Timestamp time.Time `json:"timestamp"`; Memory Memory `json:"memory"`; GPUs []GPU `json:"gpus"`; Warnings []string `json:"warnings,omitempty"` }
type Service struct{}
func New()*Service{return &Service{}}

func(s *Service)Snapshot(ctx context.Context)Snapshot{snap:=Snapshot{Timestamp:time.Now().UTC()};mem,err:=systemMemory();if err!=nil{snap.Warnings=append(snap.Warnings,err.Error())}else{snap.Memory=mem};if gpus,err:=nvidia(ctx);err==nil&&len(gpus)>0{snap.GPUs=append(snap.GPUs,gpus...)}else if err!=nil&&execExists("nvidia-smi"){snap.Warnings=append(snap.Warnings,"NVIDIA telemetry: "+err.Error())};if gpus,err:=amd(ctx);err==nil&&len(gpus)>0{snap.GPUs=append(snap.GPUs,gpus...)}else if err!=nil&&execExists("rocm-smi"){snap.Warnings=append(snap.Warnings,"AMD telemetry: "+err.Error())};return snap}

func systemMemory()(Memory,error){f,err:=os.Open("/proc/meminfo");if err!=nil{return Memory{},fmt.Errorf("read system memory: %w",err)};defer f.Close();var total,available uint64;scan:=bufio.NewScanner(f);for scan.Scan(){fields:=strings.Fields(scan.Text());if len(fields)<2{continue};value,_:=strconv.ParseUint(fields[1],10,64);switch strings.TrimSuffix(fields[0],":"){case "MemTotal":total=value*1024;case "MemAvailable":available=value*1024}};if err:=scan.Err();err!=nil{return Memory{},err};if total==0{return Memory{},fmt.Errorf("MemTotal unavailable")};return Memory{TotalBytes:total,AvailableBytes:available},nil}
func nvidia(ctx context.Context)([]GPU,error){cmd:=exec.CommandContext(ctx,"nvidia-smi","--query-gpu=index,uuid,name,memory.total,memory.free,utilization.gpu","--format=csv,noheader,nounits");out,err:=cmd.Output();if err!=nil{return nil,err};var result []GPU;for _,line:=range strings.Split(strings.TrimSpace(string(out)),"\n"){if strings.TrimSpace(line)==""{continue};parts:=strings.Split(line,",");if len(parts)<6{continue};for i:=range parts{parts[i]=strings.TrimSpace(parts[i])};idx,_:=strconv.Atoi(parts[0]);total,_:=strconv.ParseUint(parts[3],10,64);free,_:=strconv.ParseUint(parts[4],10,64);util,_:=strconv.ParseFloat(parts[5],64);result=append(result,GPU{ID:parts[1],Index:idx,Vendor:"nvidia",Name:parts[2],TotalVRAM:total*1024*1024,FreeVRAM:free*1024*1024,Utilization:util})};return result,nil}
func amd(ctx context.Context)([]GPU,error){cmd:=exec.CommandContext(ctx,"rocm-smi","--showuniqueid","--showproductname","--showmeminfo","vram","--showuse","--json");out,err:=cmd.Output();if err!=nil{return nil,err};var raw map[string]map[string]any;if err:=json.Unmarshal(out,&raw);err!=nil{return nil,err};var result []GPU;index:=0;for card,values:=range raw{name:=stringValue(values,"Card series");if name==""{name=stringValue(values,"Card model")};id:=stringValue(values,"Unique ID");if id==""{id=card};total:=uintValue(values,"VRAM Total Memory (B)");used:=uintValue(values,"VRAM Total Used Memory (B)");util:=floatValue(values,"GPU use (%)");free:=uint64(0);if total>used{free=total-used};result=append(result,GPU{ID:id,Index:index,Vendor:"amd",Name:name,TotalVRAM:total,FreeVRAM:free,Utilization:util});index++};return result,nil}
func execExists(name string)bool{_,err:=exec.LookPath(name);return err==nil}
func stringValue(m map[string]any,key string)string{if v,ok:=m[key];ok{return fmt.Sprint(v)};return ""}
func uintValue(m map[string]any,key string)uint64{value:=strings.TrimSpace(stringValue(m,key));value=strings.TrimSuffix(value," B");v,_:=strconv.ParseUint(value,10,64);return v}
func floatValue(m map[string]any,key string)float64{value:=strings.TrimSpace(stringValue(m,key));value=strings.TrimSuffix(value,"%");v,_:=strconv.ParseFloat(value,64);return v}
