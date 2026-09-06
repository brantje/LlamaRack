package gateway

import (
	"net/http"
	"strings"
)

type bodyKind int

const (
	bodyNone bodyKind = iota
	bodyJSON
	bodyMultipart
)

type metricKind int

const (
	metricNone metricKind = iota
	metricPromptOnly
	metricGeneration
)

type routeKind int

const (
	routeUnknown routeKind = iota
	routeListModels
	routeGetModel
	routeJSONProxy
	routeMultipartProxy
	routeGetResponse
	routeDeleteResponse
	routeGetInputItems
	routeCancelResponse
	routeChatControl
	routeSlotsProxy
)

type routeSpec struct {
	Method              string
	Pattern             string
	Kind                routeKind
	Body                bodyKind
	NeedsModel          bool
	NeedsAcquire        bool
	ManagerLocal        bool
	StreamingPossible   bool
	CallType            string
	Metrics             metricKind
	MapNotImplemented   bool
	CaptureResponseID   bool
	CaptureCompletionID bool
}

type routeDef struct {
	method  string
	pattern string
	spec    routeSpec
}

var classifiedRoutes = []routeDef{
	{http.MethodGet, "/v1/models", routeSpec{Kind: routeListModels, Body: bodyNone, ManagerLocal: true}},
	{http.MethodGet, "/v1/models/{model}", routeSpec{Kind: routeGetModel, Body: bodyNone, ManagerLocal: true}},
	{http.MethodPost, "/v1/chat/completions/input_tokens", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true,
		CallType: "chat_input_tokens", Metrics: metricPromptOnly, MapNotImplemented: true,
	}},
	{http.MethodPost, "/v1/chat/completions/control", routeSpec{
		Kind: routeChatControl, Body: bodyJSON, CallType: "chat_control", Metrics: metricNone,
	}},
	{http.MethodPost, "/v1/chat/completions", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true, StreamingPossible: true,
		CallType: "chat_completion", Metrics: metricGeneration, CaptureCompletionID: true,
	}},
	{http.MethodPost, "/v1/completions", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true, StreamingPossible: true,
		CallType: "completion", Metrics: metricGeneration,
	}},
	{http.MethodPost, "/v1/responses/input_tokens", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true,
		CallType: "response_input_tokens", Metrics: metricPromptOnly, MapNotImplemented: true,
	}},
	{http.MethodGet, "/v1/responses/{response_id}/input_items", routeSpec{
		Kind: routeGetInputItems, Body: bodyNone, ManagerLocal: true, CallType: "response_input_items",
	}},
	{http.MethodPost, "/v1/responses/{response_id}/cancel", routeSpec{
		Kind: routeCancelResponse, Body: bodyNone, ManagerLocal: true, CallType: "response_cancel",
	}},
	{http.MethodGet, "/v1/responses/{response_id}", routeSpec{
		Kind: routeGetResponse, Body: bodyNone, ManagerLocal: true, CallType: "response_retrieve",
	}},
	{http.MethodDelete, "/v1/responses/{response_id}", routeSpec{
		Kind: routeDeleteResponse, Body: bodyNone, ManagerLocal: true, CallType: "response_delete",
	}},
	{http.MethodPost, "/v1/responses", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true, StreamingPossible: true,
		CallType: "response", Metrics: metricGeneration, CaptureResponseID: true,
	}},
	{http.MethodPost, "/v1/embeddings", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true,
		CallType: "embedding", Metrics: metricPromptOnly,
	}},
	{http.MethodPost, "/v1/rerank", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true,
		CallType: "rerank", Metrics: metricPromptOnly,
	}},
	{http.MethodPost, "/v1/reranking", routeSpec{
		Kind: routeJSONProxy, Body: bodyJSON, NeedsModel: true, NeedsAcquire: true,
		CallType: "rerank", Metrics: metricPromptOnly,
	}},
	{http.MethodPost, "/v1/audio/transcriptions", routeSpec{
		Kind: routeMultipartProxy, Body: bodyMultipart, NeedsModel: true, NeedsAcquire: true,
		CallType: "transcription", Metrics: metricNone,
	}},
	{http.MethodGet, "/v1/slots", routeSpec{
		Kind: routeSlotsProxy, Body: bodyNone, NeedsAcquire: false,
		CallType: "slots_list", MapNotImplemented: true,
	}},
	{http.MethodPost, "/v1/slots/{slot_id}", routeSpec{
		Kind: routeSlotsProxy, Body: bodyJSON, NeedsAcquire: false,
		CallType: "slots_action", MapNotImplemented: true,
	}},
}

func classify(method, path string) (routeSpec, map[string]string, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	for _, def := range classifiedRoutes {
		if def.method != method {
			continue
		}
		params, ok := matchPath(def.pattern, path)
		if !ok {
			continue
		}
		spec := def.spec
		spec.Method = def.method
		spec.Pattern = def.pattern
		return spec, params, true
	}
	return routeSpec{}, nil, false
}

func matchPath(pattern, path string) (map[string]string, bool) {
	patternParts := splitPath(pattern)
	pathParts := splitPath(path)
	if len(patternParts) != len(pathParts) {
		return nil, false
	}
	params := map[string]string{}
	for i, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			if pathParts[i] == "" || name == "" {
				return nil, false
			}
			params[name] = pathParts[i]
			continue
		}
		if part != pathParts[i] {
			return nil, false
		}
	}
	return params, true
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func callType(path string) string {
	if spec, _, ok := classify(http.MethodPost, path); ok {
		return spec.CallType
	}
	if spec, _, ok := classify(http.MethodGet, path); ok {
		return spec.CallType
	}
	if spec, _, ok := classify(http.MethodDelete, path); ok {
		return spec.CallType
	}
	return ""
}

func supported(path string) bool {
	spec, _, ok := classify(http.MethodPost, path)
	if !ok {
		return false
	}
	switch spec.Kind {
	case routeJSONProxy, routeMultipartProxy, routeChatControl:
		return true
	default:
		return false
	}
}
