package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func (g *Gateway) proxyToTarget(observed *responseObserver, r *http.Request, spec routeSpec, requestID string, record *observability.RequestRecord, instance instances.Instance, target *url.URL, body []byte, stream bool, started time.Time, promptTPS **float64, proxyPanic *any, registerActive bool) {
	if port := target.Port(); port != "" {
		setProductHeader(wHeader(observed), headerUpstreamPort, port)
	}
	if g.observability != nil {
		g.observability.Activate(instance.ID)
	}
	active := true
	defer func() {
		if active && g.observability != nil {
			g.observability.EndActive(instance.ID)
		}
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	entry := &activeRequest{
		managerRequestID: requestID,
		instanceID:       instance.ID,
		target:           target,
		cancel:           cancel,
		endpoint:         r.URL.Path,
		startedAt:        record.StartedAt,
		model:            instance.Slug,
	}
	if registerActive {
		g.active.register(entry)
		defer g.active.remove(requestID)
	}

	workerStarted := time.Now()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Authorization")

	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.Header.Del("Authorization")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
		slog.Warn("gateway worker proxy failed", "instance_id", instance.ID, "request_id", requestID, "error", proxyErr)
		record.Error = sanitizeError(proxyErr.Error())
		writeError(writer, http.StatusServiceUnavailable, "server_error", "backend_unavailable", "Model worker unavailable")
	}

	var completed *responseMetrics
	onUpstreamID := func(id string) {
		if id == "" { return }
		g.active.setUpstreamID(requestID, id)
		if spec.CaptureResponseID { g.persistOpenAIResponseID(r.Context(), requestID, id) }
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if spec.MapNotImplemented && resp.StatusCode == http.StatusNotFound {
			payload, _ := json.Marshal(map[string]any{"error": map[string]any{
				"message": "This llama.cpp worker does not implement this route", "type": "invalid_request_error", "param": nil, "code": "not_implemented",
			}})
			resp.StatusCode = http.StatusNotImplemented
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			resp.ContentLength = int64(len(payload))
			resp.Header.Set("Content-Length", strconv.Itoa(len(payload)))
			resp.Header.Set("Content-Type", "application/json")
			return nil
		}
		if stream {
			resp.Body = &idCaptureStream{ReadCloser: resp.Body, onID: onUpstreamID, captureResponse: spec.CaptureResponseID, captureCompletion: spec.CaptureCompletionID}
			return nil
		}
		tracked := &firstReadCloser{ReadCloser: resp.Body}
		payload, readErr := io.ReadAll(tracked)
		closeErr := resp.Body.Close()
		if readErr != nil { return readErr }
		if closeErr != nil { return closeErr }
		resp.Body = io.NopCloser(bytes.NewReader(payload))
		resp.ContentLength = int64(len(payload))
		resp.Header.Set("Content-Length", strconv.Itoa(len(payload)))
		if spec.CaptureResponseID { onUpstreamID(firstNonEmpty(extractJSONID(payload), parseResponseIDFromSSE(payload))) }
		if spec.CaptureCompletionID { onUpstreamID(extractJSONID(payload)) }
		metrics := calculateResponseMetrics(workerStarted, tracked.firstRead, time.Now(), parseUsage(payload))
		completed = &metrics
		addFinalMetricHeaders(resp.Header, spec.Metrics, metrics)
		return nil
	}

	func() {
		defer func() { *proxyPanic = recover() }()
		proxy.ServeHTTP(observed, r)
	}()
	finished := time.Now()
	active = false
	if g.observability != nil { g.observability.EndActive(instance.ID) }

	record.StatusCode = observed.StatusCode()
	if record.StatusCode >= 200 && record.StatusCode < 400 { record.Result = "success" } else { record.Result = "error" }
	record.FinishedAt = finished.UnixMilli()
	record.DurationMS = milliseconds(finished.Sub(started))
	responseSample := observed.Bytes()
	metrics := responseMetrics{}
	if completed != nil { metrics = *completed } else { metrics = calculateResponseMetrics(workerStarted, observed.FirstByte(), finished, parseUsage(responseSample)) }
	record.TTFTMS = metrics.ttftMS
	record.PromptTokens = metrics.promptTokens
	record.GeneratedTokens = metrics.generatedTokens
	record.TotalTokens = metrics.totalTokens
	record.TokensPerSecond = metrics.generationTPS
	*promptTPS = metrics.promptTPS
	if record.Result == "error" && record.Error == "" { record.Error = responseError(record.StatusCode, responseSample) }
	if observed.captureAll {
		value := string(responseSample)
		record.ResponseBody = &value
	}
	if *proxyPanic != nil { panic(*proxyPanic) }
}

func (g *Gateway) persistOpenAIResponseID(ctx context.Context, requestID, openaiID string) {
	if g.observability == nil || strings.TrimSpace(openaiID) == "" { return }
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.SetOpenAIResponseID(persistCtx, requestID, openaiID); err != nil && !errors.Is(err, observability.ErrDuplicateOpenAIResponseID) {
		slog.Warn("persist openai response id failed", "request_id", requestID, "openai_response_id", openaiID, "error", err)
	} else if errors.Is(err, observability.ErrDuplicateOpenAIResponseID) {
		slog.Warn("duplicate openai response id ignored", "request_id", requestID, "openai_response_id", openaiID)
	}
}

type idCaptureStream struct {
	io.ReadCloser
	buf               []byte
	onID              func(string)
	seen              bool
	captureResponse   bool
	captureCompletion bool
}

func (s *idCaptureStream) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	if n > 0 && !s.seen {
		s.buf = append(s.buf, p[:n]...)
		id := ""
		if s.captureResponse { id = parseResponseIDFromSSE(s.buf) }
		if id == "" && s.captureCompletion {
			id = extractJSONID(s.buf)
			if id == "" { id = parseResponseIDFromSSE(s.buf) }
		}
		if id == "" {
			id = extractQuotedID(s.buf)
			if s.captureResponse && id != "" && !strings.HasPrefix(id, "resp_") { id = "" }
		}
		if id != "" {
			s.seen = true
			s.onID(id)
		}
		if len(s.buf) > 1<<20 { s.buf = s.buf[len(s.buf)/2:] }
	}
	return n, err
}
