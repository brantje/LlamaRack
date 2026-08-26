package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type HFModel struct {
	ID           string   `json:"id"`
	Author       string   `json:"author,omitempty"`
	Downloads    int64    `json:"downloads"`
	Likes        int64    `json:"likes"`
	LastModified string   `json:"last_modified,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type HFFile struct {
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	Quantization string `json:"quantization,omitempty"`
	IsGGUF       bool   `json:"is_gguf"`
}

type HuggingFace struct {
	client *http.Client
	token  string
}

func NewHuggingFace(token string) *HuggingFace {
	return &HuggingFace{client:&http.Client{Timeout:30*time.Second},token:strings.TrimSpace(token)}
}

func (h *HuggingFace) Search(ctx context.Context, query string, limit int) ([]HFModel,error) {
	if limit<=0||limit>100{limit=30}
	values:=url.Values{}
	values.Set("search",query)
	values.Set("filter","gguf")
	values.Set("sort","downloads")
	values.Set("direction","-1")
	values.Set("limit",fmt.Sprint(limit))
	var raw []struct{ ID string `json:"id"`; Author string `json:"author"`; Downloads int64 `json:"downloads"`; Likes int64 `json:"likes"`; LastModified string `json:"lastModified"`; Tags []string `json:"tags"` }
	if err:=h.getJSON(ctx,"https://huggingface.co/api/models?"+values.Encode(),&raw);err!=nil{return nil,err}
	out:=make([]HFModel,0,len(raw));for _,m:=range raw{out=append(out,HFModel{ID:m.ID,Author:m.Author,Downloads:m.Downloads,Likes:m.Likes,LastModified:m.LastModified,Tags:m.Tags})};return out,nil
}

func (h *HuggingFace) Files(ctx context.Context, repo string) ([]HFFile,error) {
	if !validRepo(repo){return nil,fmt.Errorf("invalid Hugging Face repository id")}
	var raw struct{ Siblings []struct{ RFilename string `json:"rfilename"`; Size int64 `json:"size"`; LFS *struct{Size int64 `json:"size"`} `json:"lfs"` } `json:"siblings"` }
	if err:=h.getJSON(ctx,"https://huggingface.co/api/models/"+repo+"?blobs=true",&raw);err!=nil{return nil,err}
	out:=make([]HFFile,0)
	for _,f:=range raw.Siblings{if !strings.HasSuffix(strings.ToLower(f.RFilename),".gguf"){continue};size:=f.Size;if f.LFS!=nil&&f.LFS.Size>0{size=f.LFS.Size};out=append(out,HFFile{Name:f.RFilename,Size:size,Quantization:quantizationFromName(f.RFilename),IsGGUF:true})}
	sort.Slice(out,func(i,j int)bool{return out[i].Name<out[j].Name});return out,nil
}

func (h *HuggingFace) DownloadURL(repo,file,revision string)(string,error){
	if !validRepo(repo)||strings.Contains(file,"..")||strings.HasPrefix(file,"/"){return "",fmt.Errorf("invalid Hugging Face path")};if revision==""{revision="main"}
	return "https://huggingface.co/"+repo+"/resolve/"+url.PathEscape(revision)+"/"+strings.ReplaceAll(file," ","%20")+"?download=true",nil
}

func (h *HuggingFace) ApplyAuth(req *http.Request){if h.token!=""{req.Header.Set("Authorization","Bearer "+h.token)}}
func (h *HuggingFace) getJSON(ctx context.Context,target string,out any)error{req,err:=http.NewRequestWithContext(ctx,http.MethodGet,target,nil);if err!=nil{return err};h.ApplyAuth(req);resp,err:=h.client.Do(req);if err!=nil{return err};defer resp.Body.Close();if resp.StatusCode<200||resp.StatusCode>=300{return fmt.Errorf("Hugging Face returned %s",resp.Status)};return json.NewDecoder(resp.Body).Decode(out)}
func validRepo(repo string)bool{parts:=strings.Split(repo,"/");return len(parts)==2&&parts[0]!=""&&parts[1]!=""&&!strings.Contains(repo,"..")}
func quantizationFromName(name string)string{upper:=strings.ToUpper(name);candidates:=[]string{"IQ1_S","IQ1_M","IQ2_XXS","IQ2_XS","IQ2_S","IQ2_M","IQ3_XXS","IQ3_XS","IQ3_S","IQ3_M","IQ4_XS","IQ4_NL","Q2_K","Q3_K_S","Q3_K_M","Q3_K_L","Q4_0","Q4_1","Q4_K_S","Q4_K_M","Q5_0","Q5_1","Q5_K_S","Q5_K_M","Q6_K","Q8_0","F16","BF16","F32"};for _,q:=range candidates{if strings.Contains(upper,q){return q}};return ""}
