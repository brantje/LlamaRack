package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brantje/llamarack/backend/internal/benchmark"
	"github.com/brantje/llamarack/backend/internal/instances"
)

type fakeBenchmarkManagementService struct {
	caps        benchmark.Capabilities
	page        benchmark.Page
	run         benchmark.Run
	err         error
	createdID   string
	createdWork *benchmark.WorkloadProfile
	lastFilter  benchmark.Filter
	cancelledID string
	deletedID   string
}

func (s *fakeBenchmarkManagementService) Capabilities(context.Context) (benchmark.Capabilities, error) { return s.caps, s.err }
func (s *fakeBenchmarkManagementService) Create(_ context.Context, id string, workload *benchmark.WorkloadProfile) (benchmark.Run, error) {
	s.createdID, s.createdWork = id, workload
	return s.run, s.err
}
func (s *fakeBenchmarkManagementService) Get(context.Context, string) (benchmark.Run, error) { return s.run, s.err }
func (s *fakeBenchmarkManagementService) List(_ context.Context, filter benchmark.Filter) (benchmark.Page, error) {
	s.lastFilter = filter
	return s.page, s.err
}
func (s *fakeBenchmarkManagementService) Cancel(_ context.Context, id string) (benchmark.Run, error) {
	s.cancelledID = id
	return s.run, s.err
}
func (s *fakeBenchmarkManagementService) Delete(_ context.Context, id string) error { s.deletedID = id; return s.err }

type fakeBenchmarkInstanceResolver struct {
	bySlug map[string]instances.Instance
	byID   map[string]instances.Instance
}
func (r fakeBenchmarkInstanceResolver) GetBySlug(_ context.Context, slug string) (instances.Instance, error) {
	if item, ok := r.bySlug[slug]; ok { return item, nil }
	return instances.Instance{}, sql.ErrNoRows
}
func (r fakeBenchmarkInstanceResolver) GetByID(_ context.Context, id string) (instances.Instance, error) {
	if item, ok := r.byID[id]; ok { return item, nil }
	return instances.Instance{}, sql.ErrNoRows
}

func benchmarkTestMux(service benchmarkManagementService, resolver benchmarkInstanceResolver) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterBenchmarkRoutes(mux, NewBenchmarkHandler(service, resolver))
	return mux
}

func benchmarkRequest(t *testing.T, mux http.Handler, method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil { t.Fatal(err) }
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if authenticated {
		req = req.WithContext(context.WithValue(req.Context(), managementAuthContextKey{}, managementAuthContext{}))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestBenchmarkManagementRoutesRequireAuthentication(t *testing.T) {
	mux := benchmarkTestMux(&fakeBenchmarkManagementService{}, fakeBenchmarkInstanceResolver{})
	w := benchmarkRequest(t, mux, http.MethodGet, "/api/v1/benchmarks", nil, false)
	if w.Code != http.StatusUnauthorized { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
}

func TestBenchmarkCreateResolvesInstanceAndAcceptsOnlyWorkload(t *testing.T) {
	service := &fakeBenchmarkManagementService{run: benchmark.Run{ID: "run-1", Status: benchmark.StatusQueued}}
	resolver := fakeBenchmarkInstanceResolver{bySlug: map[string]instances.Instance{"coder": {ID: "instance-uuid", Slug: "coder"}}}
	mux := benchmarkTestMux(service, resolver)
	w := benchmarkRequest(t, mux, http.MethodPost, "/api/v1/instances/coder/benchmarks", map[string]any{
		"workload": map[string]any{"prompt_tokens": []int{256}, "generation_tokens": []int{32}, "repetitions": 3, "warmup": true},
		"model_path": "/tmp/evil.gguf", "gpu_devices": []string{"CUDA9"},
	}, true)
	if w.Code != http.StatusCreated { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
	if service.createdID != "instance-uuid" || service.createdWork == nil || service.createdWork.Repetitions != 3 {
		t.Fatalf("created id=%q workload=%+v", service.createdID, service.createdWork)
	}
}

func TestBenchmarkListValidatesAndForwardsFilters(t *testing.T) {
	service := &fakeBenchmarkManagementService{page: benchmark.Page{Items: []benchmark.Run{}, Limit: 25}}
	mux := benchmarkTestMux(service, fakeBenchmarkInstanceResolver{})
	w := benchmarkRequest(t, mux, http.MethodGet, "/api/v1/benchmarks?instance_id=i1&model_id=m1&status=completed&limit=25&offset=5", nil, true)
	if w.Code != http.StatusOK { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
	if service.lastFilter.InstanceID != "i1" || service.lastFilter.ModelID != "m1" || service.lastFilter.Status != benchmark.StatusCompleted || service.lastFilter.Limit != 25 || service.lastFilter.Offset != 5 {
		t.Fatalf("filter=%+v", service.lastFilter)
	}
	for _, path := range []string{"/api/v1/benchmarks?status=nope", "/api/v1/benchmarks?limit=101", "/api/v1/benchmarks?offset=-1"} {
		if got := benchmarkRequest(t, mux, http.MethodGet, path, nil, true); got.Code != http.StatusBadRequest { t.Fatalf("%s status=%d", path, got.Code) }
	}
}

func TestBenchmarkDetailCancelDeleteAndErrorMapping(t *testing.T) {
	service := &fakeBenchmarkManagementService{run: benchmark.Run{ID: "run-1", Status: benchmark.StatusRunning}}
	mux := benchmarkTestMux(service, fakeBenchmarkInstanceResolver{})
	if w := benchmarkRequest(t, mux, http.MethodGet, "/api/v1/benchmarks/run-1", nil, true); w.Code != http.StatusOK { t.Fatalf("get=%d", w.Code) }
	if w := benchmarkRequest(t, mux, http.MethodPost, "/api/v1/benchmarks/run-1/cancel", nil, true); w.Code != http.StatusAccepted || service.cancelledID != "run-1" { t.Fatalf("cancel=%d id=%q", w.Code, service.cancelledID) }
	if w := benchmarkRequest(t, mux, http.MethodDelete, "/api/v1/benchmarks/run-1", nil, true); w.Code != http.StatusNoContent || service.deletedID != "run-1" { t.Fatalf("delete=%d id=%q", w.Code, service.deletedID) }

	for err, want := range map[error]int{
		benchmark.ErrNotFound: http.StatusNotFound,
		benchmark.ErrInvalidWorkload: http.StatusBadRequest,
		benchmark.ErrInsufficientResources: http.StatusConflict,
		benchmark.ErrUnavailable: http.StatusServiceUnavailable,
		errors.New("boom"): http.StatusInternalServerError,
	} {
		service.err = err
		if w := benchmarkRequest(t, mux, http.MethodGet, "/api/v1/benchmarks/run-1", nil, true); w.Code != want { t.Fatalf("err=%v status=%d want=%d", err, w.Code, want) }
	}
}
