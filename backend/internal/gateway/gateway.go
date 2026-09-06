package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/observability"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
)

const (
	metadataResponseCaptureLimit = 8 << 20
	preAuthRequestBodyBytes      = 64 << 10
	maxRequestBodyBytes          = 32 << 20
	modelSlugCaptureLimit        = 512
)

var requestIDFallback atomic.Uint64

type gatewayAllowlistKey struct{}

type gatewayAllowlist struct {
	all bool
	ids map[string]struct{}
}

type Gateway struct {
	auth          *auth.Service
	lifecycle     *lifecycle.Service
	observability *observability.Service
	network       *managersecurity.Network
	active        *activeRegistry
}

type requestEnvelope struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	SessionID string `json:"session_id"`
	Metadata  struct {
		SessionID string `json:"session_id"`
	} `json:"metadata"`
	LiteLLMMetadata struct {
		TraceID   string `json:"trace_id"`
		SessionID string `json:"session_id"`
	} `json:"litellm_metadata"`
}

func New(a *auth.Service, _ *models.Service, l *lifecycle.Service, services ...*observability.Service) *Gateway {
	if l != nil {
		l.Instances().EnableHotCache()
	}
	g := &Gateway{auth: a, lifecycle: l, active: newActiveRegistry()}
	if len(services) > 0 {
		g.observability = services[0]
	}
	return g
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := newRequestID()
	setProductHeader(w.Header(), headerRequestID, requestID)
	spec, params, known := classify(r.Method, r.URL.Path)
	if spec.CallType == "" && known {
		spec.CallType = callType(r.URL.Path)
	}

	traceID := resolveTraceID(r, "")
	w.Header().Set(headerTraceID, traceID)

	callTypeValue := spec.CallType
	if !known {
		callTypeValue = ""
	}
	record := observability.RequestRecord{
		StartedAt: started.UnixMilli(), Endpoint: r.URL.Path, TraceID: traceID,
		CallType: callTypeValue, ClientIP: g.clientIP(r), UserAgent: boundedMetadata(r.UserAgent(), 2048),
	}
	observed := newResponseObserver(w, false)
	g.begin(r.Context(), requestID, record)

	var promptTPS *float64
	var proxyPanic any
	defer func() {
		if recovered := recover(); recovered != nil {
			proxyPanic = recovered
		}
		finished := time.Now()
		if record.FinishedAt == 0 {
			record.FinishedAt = finished.UnixMilli()
		}
		if record.DurationMS == 0 {
			record.DurationMS = milliseconds(finished.Sub(started))
		}
		if record.StatusCode == 0 {
			record.StatusCode = observed.StatusCode()
		}
		if proxyPanic != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Result = "error"
			if record.Error == "" {
				record.Error = "Proxy stream aborted"
			}
		}
		if ctxErr := r.Context().Err(); ctxErr != nil {
			record.Result = "error"
			if record.Error == "" {
				record.Error = sanitizeError("client disconnected: " + ctxErr.Error())
			}
		}
		if record.Result == "" {
			if record.StatusCode >= 200 && record.StatusCode < 400 {
				record.Result = "success"
			} else {
				record.Result = "error"
			}
		}
		if record.Result == "error" && record.Error == "" {
			record.Error = responseError(record.StatusCode, observed.Bytes())
		}
		if observed.captureAll && record.ResponseBody == nil {
			value := string(observed.Bytes())
			record.ResponseBody = &value
		}
		g.finalize(r.Context(), requestID, promptTPS, record)
		if proxyPanic != nil {
			panic(proxyPanic)
		}
	}()

	key, err := g.authenticateKey(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeError(observed, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
		return
	}
	if key.KeyType == auth.APIKeyTypeManagement {
		writeError(observed, http.StatusForbidden, "permission_error", "management_key_not_allowed", "This API key cannot access the inference API")
		return
	}
	record.APIKey = &observability.APIKeyRef{ID: key.ID, Name: key.Name, Prefix: key.Prefix}
	r = stampResponseOwner(r, &record, key)

	allowAll, allowedIDs, allStale, allowErr := g.inferenceAllowlist(r.Context(), key)
	if allowErr != nil {
		writeError(observed, http.StatusInternalServerError, "server_error", "database_error", "Unable to list models")
		return
	}
	if allStale {
		writeError(observed, http.StatusForbidden, "permission_error", "api_key_instances_unavailable", "None of this API key's allowed instances still exist")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), gatewayAllowlistKey{}, gatewayAllowlist{all: allowAll, ids: allowedIDs}))

	var body []byte
	var envelope requestEnvelope
	var parseErr error
	var bodyReadErr error
	bodyTooLarge := false
	if spec.Body != bodyNone {
		body, bodyReadErr, bodyTooLarge = readBoundedRequestBody(r.Body)
		if spec.Body == bodyJSON && bodyReadErr == nil && !bodyTooLarge {
			if looksLikeJSON(body) {
				parseErr = json.Unmarshal(body, &envelope)
			} else if len(bytes.TrimSpace(body)) > 0 {
				parseErr = errors.New("invalid json")
			}
			record.Streaming = envelope.Stream
			recordSessionCapture(r, envelope)
			g.captureModelSlug(r.Context(), requestID, strings.TrimSpace(envelope.Model))
			if suppliedTraceID, ok := suppliedTraceID(r, envelope.LiteLLMMetadata.TraceID); ok && suppliedTraceID != traceID {
				record.TraceID = suppliedTraceID
				w.Header().Set(headerTraceID, suppliedTraceID)
			}
		}
	}
	g.update(r.Context(), requestID, record)

	if !known {
		writeError(observed, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown OpenAI-compatible endpoint")
		return
	}
	if spec.Body != bodyNone {
		if bodyReadErr != nil {
			writeError(observed, http.StatusBadRequest, "invalid_request_error", "invalid_body", "Invalid request body")
			return
		}
		if bodyTooLarge {
			writeError(observed, http.StatusRequestEntityTooLarge, "invalid_request_error", "body_too_large", "Request body is too large")
			return
		}
	}

	switch spec.Kind {
	case routeListModels:
		g.listModels(observed, r, allowAll, allowedIDs)
	case routeGetModel:
		g.getModel(observed, r, requestID, &record, params["model"])
	case routeGetResponse:
		g.getStoredResponse(observed, r, params["response_id"])
	case routeDeleteResponse:
		g.deleteStoredResponse(observed, r, params["response_id"])
	case routeGetInputItems:
		g.getResponseInputItems(observed, r, params["response_id"])
	case routeCancelResponse:
		g.cancelStoredResponse(observed, r, params["response_id"])
	case routeChatControl:
		g.proxyChatControl(observed, r, spec, requestID, &record, body, started, &promptTPS, &proxyPanic)
	case routeMultipartProxy:
		g.proxyMultipart(observed, r, spec, requestID, &record, body, started, &promptTPS, &proxyPanic)
	case routeJSONProxy:
		if parseErr != nil || strings.TrimSpace(envelope.Model) == "" {
			writeError(observed, http.StatusBadRequest, "invalid_request_error", "model_required", "A model ID is required")
			return
		}
		g.proxyJSON(observed, r, spec, requestID, &record, envelope, body, started, &promptTPS, &proxyPanic)
	case routeSlotsProxy:
		if spec.Body == bodyJSON && parseErr != nil {
			writeError(observed, http.StatusBadRequest, "invalid_request_error", "invalid_body", "Invalid request body")
			return
		}
		g.proxySlots(observed, r, spec, params, requestID, &record, body, &proxyPanic)
	default:
		writeError(observed, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown OpenAI-compatible endpoint")
	}
}

func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func readBoundedRequestBody(body io.Reader) ([]byte, error, bool) {
	if body == nil {
		return nil, nil, false
	}
	payload, err := io.ReadAll(io.LimitReader(body, int64(maxRequestBodyBytes)+1))
	if len(payload) > maxRequestBodyBytes {
		return nil, err, true
	}
	return payload, err, false
}

func (g *Gateway) persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	if g.observability != nil && g.observability.WritebackEnabled() {
		return detached, func() {}
	}
	return context.WithTimeout(detached, 5*time.Second)
}

func (g *Gateway) begin(ctx context.Context, requestID string, record observability.RequestRecord) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.BeginCorrelatedRequest(persistCtx, requestID, record); err != nil {
		slog.Warn("begin inference observability failed", "request_id", requestID, "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

func (g *Gateway) update(ctx context.Context, requestID string, record observability.RequestRecord) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.UpdateCorrelatedRequest(persistCtx, requestID, record); err != nil {
		slog.Warn("update inference observability failed", "request_id", requestID, "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

func (g *Gateway) finalize(ctx context.Context, requestID string, promptTPS *float64, record observability.RequestRecord) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.FinalizeCorrelatedRequest(persistCtx, requestID, promptTPS, record); err != nil {
		slog.Warn("finalize inference observability failed", "request_id", requestID, "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

func (g *Gateway) captureModelSlug(ctx context.Context, requestID, slug string) {
	slug = boundedMetadata(slug, modelSlugCaptureLimit)
	if g.observability == nil || slug == "" {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.SetRequestModelSlug(persistCtx, requestID, slug); err != nil {
		slog.Warn("capture inference model slug failed", "request_id", requestID, "model_slug", slug, "error", err)
	}
}

func (g *Gateway) persist(ctx context.Context, requestID string, promptTPS *float64, record observability.RequestRecord) {
	g.finalize(ctx, requestID, promptTPS, record)
}

func (g *Gateway) authenticate(ctx context.Context, header string) error {
	_, err := g.authenticateKey(ctx, header)
	return err
}

func (g *Gateway) authenticateKey(ctx context.Context, header string) (auth.APIKey, error) {
	if !strings.HasPrefix(header, "Bearer ") {
		return auth.APIKey{}, errors.New("missing bearer token")
	}
	return g.auth.AuthenticateAPIKeyInfo(ctx, strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
}
