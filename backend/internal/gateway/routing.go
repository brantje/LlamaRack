package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/observability"
	"github.com/brantje/llamarack/backend/internal/slots"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func (g *Gateway) listModels(w http.ResponseWriter, r *http.Request, allowAll bool, allowedIDs map[string]struct{}) {
	items, err := g.lifecycle.Instances().List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "database_error", "Unable to list models")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, instance := range items {
		if instance.Enabled && g.instanceAllowed(instance.ID, allowAll, allowedIDs) {
			out = append(out, openaiModelObject(instance.Slug))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

func (g *Gateway) getModel(w http.ResponseWriter, r *http.Request, modelSlug string) {
	instance, err := g.lifecycle.Instances().GetBySlug(r.Context(), strings.TrimSpace(modelSlug))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
		} else {
			writeError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		}
		return
	}
	if !requestInstanceAllowed(r, instance.ID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	if !instance.Enabled {
		writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
		return
	}
	writeJSON(w, http.StatusOK, openaiModelObject(instance.Slug))
}

func openaiModelObject(id string) map[string]any {
	return map[string]any{"id": id, "object": "model", "created": time.Now().Unix(), "owned_by": "llamarack"}
}

func (g *Gateway) proxyJSON(observed *responseObserver, r *http.Request, spec routeSpec, requestID string, record *observability.RequestRecord, envelope requestEnvelope, body []byte, started time.Time, promptTPS **float64, proxyPanic *any) {
	instance, ok := g.resolveInstanceBySlug(observed, r, record, strings.TrimSpace(envelope.Model))
	if !ok { return }
	g.captureModelSlug(r.Context(), requestID, instance.Slug)
	if instance.RequestLogMode == "full" {
		value := string(body)
		record.RequestBody = &value
		observed.captureAll = true
	}
	g.proxyAcquired(observed, r, spec, requestID, record, instance, body, envelope.Stream, started, promptTPS, proxyPanic)
}

func (g *Gateway) proxyMultipart(observed *responseObserver, r *http.Request, spec routeSpec, requestID string, record *observability.RequestRecord, body []byte, started time.Time, promptTPS **float64, proxyPanic *any) {
	model, logJSON, err := parseMultipartModel(body, r.Header.Get("Content-Type"))
	if err != nil {
		writeError(observed, http.StatusBadRequest, "invalid_request_error", "invalid_body", "Invalid multipart body")
		return
	}
	if strings.TrimSpace(model) == "" {
		writeError(observed, http.StatusBadRequest, "invalid_request_error", "model_required", "A model ID is required")
		return
	}
	g.captureModelSlug(r.Context(), requestID, model)
	instance, ok := g.resolveInstanceBySlug(observed, r, record, model)
	if !ok { return }
	if instance.RequestLogMode == "full" {
		record.RequestBody = &logJSON
		observed.captureAll = true
	}
	g.proxyAcquired(observed, r, spec, requestID, record, instance, body, false, started, promptTPS, proxyPanic)
}

func (g *Gateway) proxyChatControl(observed *responseObserver, r *http.Request, spec routeSpec, requestID string, record *observability.RequestRecord, body []byte, started time.Time, promptTPS **float64, proxyPanic *any) {
	var envelope struct { ID string `json:"id"` }
	_ = json.Unmarshal(body, &envelope)
	active := g.active.getByUpstream(strings.TrimSpace(envelope.ID))
	if active == nil || active.target == nil {
		writeError(observed, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown in-flight completion")
		return
	}
	instance, ok := g.resolveInstanceByID(observed, r, record, active.instanceID)
	if !ok { return }
	g.captureModelSlug(r.Context(), requestID, active.model)
	if instance.RequestLogMode == "full" {
		value := string(body)
		record.RequestBody = &value
		observed.captureAll = true
	}
	g.proxyToTarget(observed, r, spec, requestID, record, instance, active.target, body, false, started, promptTPS, proxyPanic, false)
}

func (g *Gateway) proxySlots(observed *responseObserver, r *http.Request, spec routeSpec, params map[string]string, requestID string, record *observability.RequestRecord, body []byte, proxyPanic *any) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeError(observed, http.StatusBadRequest, "invalid_request_error", "model_required", "A model ID is required")
		return
	}
	g.captureModelSlug(r.Context(), requestID, model)
	instance, ok := g.resolveInstanceBySlug(observed, r, record, model)
	if !ok { return }
	setProductHeader(wHeader(observed), headerInstance, instance.Slug)

	upstreamPath := slots.UpstreamPath(r.Method, params["slot_id"])
	if r.Method == http.MethodPost {
		action := strings.TrimSpace(r.URL.Query().Get("action"))
		if err := slots.ValidateAction(action); err != nil {
			writeError(observed, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error())
			return
		}
		validated, err := slots.ValidateRequestBody(body, action)
		if err != nil {
			writeError(observed, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error())
			return
		}
		body = validated
	}

	endpoint, ready := g.lifecycle.RuntimeEndpoint(instance.ID)
	if !ready {
		record.Error = "instance unloaded"
		writeError(observed, http.StatusServiceUnavailable, "server_error", "model_unavailable", "instance unloaded and autoload disabled")
		return
	}
	func() {
		defer func() { *proxyPanic = recover() }()
		if err := slots.Proxy(observed, r, endpoint, upstreamPath, body, spec.MapNotImplemented); err != nil {
			record.Error = "Invalid worker endpoint"
			writeError(observed, http.StatusInternalServerError, "server_error", "invalid_worker_endpoint", "Invalid worker endpoint")
		}
	}()
}

func (g *Gateway) resolveInstanceBySlug(observed *responseObserver, r *http.Request, record *observability.RequestRecord, slug string) (instances.Instance, bool) {
	instance, err := g.lifecycle.Instances().GetBySlug(r.Context(), slug)
	if err != nil {
		record.Error = sanitizeError(err.Error())
		if errors.Is(err, sql.ErrNoRows) {
			writeError(observed, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
		} else {
			writeError(observed, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		}
		return instances.Instance{}, false
	}
	return g.authorizeResolvedInstance(observed, r, record, instance)
}

func (g *Gateway) resolveInstanceByID(observed *responseObserver, r *http.Request, record *observability.RequestRecord, id string) (instances.Instance, bool) {
	instance, err := g.lifecycle.Instances().GetByID(r.Context(), id)
	if err != nil {
		record.Error = sanitizeError(err.Error())
		if errors.Is(err, sql.ErrNoRows) {
			writeError(observed, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
		} else {
			writeError(observed, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		}
		return instances.Instance{}, false
	}
	return g.authorizeResolvedInstance(observed, r, record, instance)
}

func (g *Gateway) authorizeResolvedInstance(observed *responseObserver, r *http.Request, record *observability.RequestRecord, instance instances.Instance) (instances.Instance, bool) {
	record.InstanceID = instance.ID
	if !requestInstanceAllowed(r, instance.ID) {
		writeError(observed, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return instances.Instance{}, false
	}
	return instance, true
}

func (g *Gateway) inferenceAllowlist(ctx context.Context, key auth.APIKey) (allowAll bool, allowed map[string]struct{}, allStale bool, err error) {
	if key.KeyType != auth.APIKeyTypeInference || len(key.InstanceIDs) == 0 {
		return true, nil, false, nil
	}
	items, err := g.lifecycle.Instances().List(ctx)
	if err != nil { return false, nil, false, err }
	live := map[string]struct{}{}
	for _, item := range items { live[item.ID] = struct{}{} }
	allowed = map[string]struct{}{}
	for _, id := range key.InstanceIDs {
		if _, ok := live[id]; ok { allowed[id] = struct{}{} }
	}
	if len(allowed) == 0 { return false, allowed, true, nil }
	return false, allowed, false, nil
}

func (g *Gateway) instanceAllowed(instanceID string, allowAll bool, allowedIDs map[string]struct{}) bool {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" { return false }
	if allowAll { return true }
	_, ok := allowedIDs[instanceID]
	return ok
}

func requestInstanceAllowed(r *http.Request, instanceID string) bool {
	value, ok := r.Context().Value(gatewayAllowlistKey{}).(gatewayAllowlist)
	if !ok || value.all { return true }
	_, allowed := value.ids[strings.TrimSpace(instanceID)]
	return allowed
}

func (g *Gateway) proxyAcquired(observed *responseObserver, r *http.Request, spec routeSpec, requestID string, record *observability.RequestRecord, instance instances.Instance, body []byte, stream bool, started time.Time, promptTPS **float64, proxyPanic *any) {
	record.InstanceID = instance.ID
	setProductHeader(wHeader(observed), headerInstance, instance.Slug)
	preRuntime, _ := g.lifecycle.RuntimeInstance(r.Context(), instance.ID)
	record.Autoloaded = preRuntime.State != supervisor.Ready
	setProductHeader(wHeader(observed), headerAutoloaded, strconv.FormatBool(record.Autoloaded))
	g.update(r.Context(), requestID, *record)

	if g.observability != nil { g.observability.Queue(instance.ID) }
	queueStarted := time.Now()
	endpoint, release, err := g.lifecycle.Acquire(r.Context(), instance.ID)
	record.QueueDurationMS = milliseconds(time.Since(queueStarted))
	setProductHeader(wHeader(observed), headerQueueMS, metricFloat(record.QueueDurationMS))
	if record.Autoloaded {
		record.LoadDurationMS = record.QueueDurationMS
		setProductHeader(wHeader(observed), headerLoadMS, metricFloat(record.LoadDurationMS))
	}
	if err != nil {
		if g.observability != nil { g.observability.EndQueued(instance.ID) }
		record.Error = sanitizeError(err.Error())
		if errors.Is(err, lifecycle.ErrQueueLimitExceeded) {
			scope := lifecycle.QueueLimitScope(err)
			if g.observability != nil {
				_ = g.observability.RecordQueueLimitRejection(r.Context(), instance.ID, scope)
			}
			writeError(observed, http.StatusServiceUnavailable, "server_error", "overloaded", err.Error())
			return
		}
		writeError(observed, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		return
	}
	defer release()
	target, err := url.Parse(endpoint)
	if err != nil {
		if g.observability != nil { g.observability.EndQueued(instance.ID) }
		record.Error = "Invalid worker endpoint"
		writeError(observed, http.StatusInternalServerError, "server_error", "invalid_worker_endpoint", "Invalid worker endpoint")
		return
	}
	g.proxyToTarget(observed, r, spec, requestID, record, instance, target, body, stream, started, promptTPS, proxyPanic, true)
}

func wHeader(observed *responseObserver) http.Header { return observed.Header() }
