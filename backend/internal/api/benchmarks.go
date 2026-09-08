package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/benchmark"
	"github.com/brantje/llamarack/backend/internal/instances"
)

type benchmarkManagementService interface {
	Capabilities(context.Context) (benchmark.Capabilities, error)
	Create(context.Context, string, *benchmark.WorkloadProfile) (benchmark.Run, error)
	Get(context.Context, string) (benchmark.Run, error)
	List(context.Context, benchmark.Filter) (benchmark.Page, error)
	Cancel(context.Context, string) (benchmark.Run, error)
	Delete(context.Context, string) error
}

type benchmarkInstanceResolver interface {
	GetBySlug(context.Context, string) (instances.Instance, error)
	GetByID(context.Context, string) (instances.Instance, error)
}

type benchmarkHandler struct {
	service   benchmarkManagementService
	instances benchmarkInstanceResolver
}

func NewBenchmarkHandler(service benchmarkManagementService, instanceResolver benchmarkInstanceResolver) http.Handler {
	return &benchmarkHandler{service: service, instances: instanceResolver}
}

func RegisterBenchmarkRoutes(mux *http.ServeMux, handler http.Handler) {
	mux.Handle("GET /api/v1/benchmarks", handler)
	mux.Handle("GET /api/v1/benchmarks/capabilities", handler)
	mux.Handle("POST /api/v1/instances/{id}/benchmarks", handler)
	mux.Handle("GET /api/v1/benchmarks/{id}", handler)
	mux.Handle("POST /api/v1/benchmarks/{id}/cancel", handler)
	mux.Handle("DELETE /api/v1/benchmarks/{id}", handler)
}

func (h *benchmarkHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := managementAuthFromRequest(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if h == nil || h.service == nil || h.instances == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "benchmark service is not configured"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/benchmarks/capabilities" && r.Method == http.MethodGet:
		h.capabilities(w, r)
	case path == "/api/v1/benchmarks" && r.Method == http.MethodGet:
		h.list(w, r)
	case strings.HasPrefix(path, "/api/v1/instances/") && strings.HasSuffix(path, "/benchmarks") && r.Method == http.MethodPost:
		h.create(w, r)
	case strings.HasSuffix(path, "/cancel") && r.Method == http.MethodPost:
		h.cancel(w, r)
	case strings.HasPrefix(path, "/api/v1/benchmarks/") && r.Method == http.MethodGet:
		h.get(w, r)
	case strings.HasPrefix(path, "/api/v1/benchmarks/") && r.Method == http.MethodDelete:
		h.delete(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *benchmarkHandler) capabilities(w http.ResponseWriter, r *http.Request) {
	caps, err := h.service.Capabilities(r.Context())
	if err != nil {
		writeBenchmarkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}

func (h *benchmarkHandler) list(w http.ResponseWriter, r *http.Request) {
	filter := benchmark.Filter{InstanceID: strings.TrimSpace(r.URL.Query().Get("instance_id")), ModelID: strings.TrimSpace(r.URL.Query().Get("model_id"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		filter.Status = benchmark.Status(strings.ToUpper(raw))
		if !filter.Status.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid benchmark status"})
			return
		}
	}
	var err error
	if filter.Limit, err = benchmarkQueryInt(r, "limit", 50); err != nil || filter.Limit < 1 || filter.Limit > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 100"})
		return
	}
	if filter.Offset, err = benchmarkQueryInt(r, "offset", 0); err != nil || filter.Offset < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be zero or greater"})
		return
	}
	page, err := h.service.List(r.Context(), filter)
	if err != nil {
		writeBenchmarkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func benchmarkQueryInt(r *http.Request, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func (h *benchmarkHandler) create(w http.ResponseWriter, r *http.Request) {
	instanceID, err := h.resolveInstanceID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeBenchmarkError(w, err)
		return
	}
	var input struct {
		Workload *benchmark.WorkloadProfile `json:"workload"`
	}
	if !decode(w, r, &input) {
		return
	}
	run, err := h.service.Create(r.Context(), instanceID, input.Workload)
	if err != nil {
		writeBenchmarkError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (h *benchmarkHandler) resolveInstanceID(ctx context.Context, value string) (string, error) {
	item, err := h.instances.GetBySlug(ctx, value)
	if err == nil {
		return item.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	item, err = h.instances.GetByID(ctx, strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return item.ID, nil
}

func (h *benchmarkHandler) get(w http.ResponseWriter, r *http.Request) {
	run, err := h.service.Get(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeBenchmarkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
func (h *benchmarkHandler) cancel(w http.ResponseWriter, r *http.Request) {
	run, err := h.service.Cancel(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeBenchmarkError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}
func (h *benchmarkHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), strings.TrimSpace(r.PathValue("id"))); err != nil {
		writeBenchmarkError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeBenchmarkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, benchmark.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, benchmark.ErrInvalidWorkload), errors.Is(err, benchmark.ErrUnsupportedConfig):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, benchmark.ErrInsufficientResources), errors.Is(err, benchmark.ErrTransitionConflict):
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, benchmark.ErrUnavailable):
		writeErr(w, http.StatusServiceUnavailable, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}
