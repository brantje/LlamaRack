package main

import (
	"net/http"
	"runtime/debug"

	manageropenapi "github.com/brantje/llamacpp-manager/backend/internal/openapi"
)

type documentedRoute struct {
	method      string
	path        string
	operationID string
	summary     string
	tag         string
	security    bool
	requestBody bool
	response    string
}

func newOpenAPIDocument() *manageropenapi.Document {
	doc := manageropenapi.New(applicationVersion())
	registerManagementOperations(doc)
	registerInferenceOperations(doc)
	return doc
}

func applicationVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "development"
}

func registerManagementOperations(doc *manageropenapi.Document) {
	routes := []documentedRoute{
		{http.MethodGet, "/api/v1/health", "getManagementHealth", "Get management API health", "System", false, false, "200"},
		{http.MethodGet, "/api/v1/auth/bootstrap", "getBootstrapStatus", "Get bootstrap status", "Authentication", false, false, "200"},
		{http.MethodPost, "/api/v1/auth/bootstrap", "bootstrapManager", "Create the first management user", "Authentication", false, true, "201"},
		{http.MethodPost, "/api/v1/auth/login", "loginManager", "Log in to the manager", "Authentication", false, true, "200"},
		{http.MethodPost, "/api/v1/auth/logout", "logoutManager", "Log out of the manager", "Authentication", true, false, "204"},
		{http.MethodGet, "/api/v1/me", "getCurrentUser", "Get the current management user", "Profile", true, false, "200"},
		{http.MethodPost, "/api/v1/me/password", "changeCurrentUserPassword", "Change the current user's password", "Profile", true, true, "204"},
		{http.MethodGet, "/api/v1/me/sessions", "listCurrentUserSessions", "List the current user's sessions", "Profile", true, false, "200"},
		{http.MethodPost, "/api/v1/me/sessions/revoke-others", "revokeOtherSessions", "Revoke the current user's other sessions", "Profile", true, false, "200"},
		{http.MethodPost, "/api/v1/me/sessions/revoke-all", "revokeAllSessions", "Revoke all sessions for the current user", "Profile", true, false, "204"},
		{http.MethodGet, "/api/v1/users", "listUsers", "List management users", "Users", true, false, "200"},
		{http.MethodPost, "/api/v1/users", "createUser", "Create a management user", "Users", true, true, "201"},
		{http.MethodPatch, "/api/v1/users/{id}", "updateUser", "Enable or disable a management user", "Users", true, true, "204"},
		{http.MethodDelete, "/api/v1/users/{id}", "deleteUser", "Delete a management user", "Users", true, false, "204"},
		{http.MethodPost, "/api/v1/users/{id}/password", "resetUserPassword", "Reset a management user's password", "Users", true, true, "204"},
		{http.MethodGet, "/api/v1/users/{id}/sessions", "listUserSessions", "List a management user's sessions", "Users", true, false, "200"},
		{http.MethodDelete, "/api/v1/sessions/{id}", "revokeSession", "Revoke a management session", "Users", true, false, "204"},
		{http.MethodGet, "/api/v1/settings/general", "getGeneralSettings", "Get manager settings", "Administration", true, false, "200"},
		{http.MethodPut, "/api/v1/settings/general", "updateGeneralSettings", "Update manager settings", "Administration", true, true, "200"},
		{http.MethodGet, "/api/v1/system", "getSystemDiagnostics", "Get safe system diagnostics", "Administration", true, false, "200"},
		{http.MethodGet, "/api/v1/admin/summary", "getAdminSummary", "Get administration summary", "Administration", true, false, "200"},
		{http.MethodGet, "/api/v1/api-keys", "listAPIKeys", "List inference API keys", "API Keys", true, false, "200"},
		{http.MethodPost, "/api/v1/api-keys", "createAPIKey", "Create an inference API key", "API Keys", true, true, "201"},
		{http.MethodPatch, "/api/v1/api-keys/{id}", "updateAPIKey", "Enable or disable an inference API key", "API Keys", true, true, "204"},
		{http.MethodDelete, "/api/v1/api-keys/{id}", "deleteAPIKey", "Revoke an inference API key", "API Keys", true, false, "204"},
		{http.MethodPost, "/api/v1/api-keys/{id}/revoke", "revokeAPIKey", "Revoke an inference API key", "API Keys", true, false, "204"},
		{http.MethodPost, "/api/v1/api-keys/{id}/rotate", "rotateAPIKey", "Rotate an inference API key", "API Keys", true, false, "201"},
		{http.MethodGet, "/api/v1/models", "listModels", "List registered models", "Models", true, false, "200"},
		{http.MethodPost, "/api/v1/models", "createModel", "Register a model", "Models", true, true, "201"},
		{http.MethodGet, "/api/v1/models/available", "listAvailableModels", "List available GGUF files", "Models", true, false, "200"},
		{http.MethodGet, "/api/v1/models/{id}", "getModel", "Get a registered model", "Models", true, false, "200"},
		{http.MethodPut, "/api/v1/models/{id}", "updateModel", "Update a registered model", "Models", true, true, "200"},
		{http.MethodDelete, "/api/v1/models/{id}", "deleteModel", "Delete a registered model", "Models", true, false, "204"},
		{http.MethodGet, "/api/v1/models/{id}/options", "getModelOptions", "Get model llama.cpp options", "Models", true, false, "200"},
		{http.MethodPost, "/api/v1/models/{id}/start", "startModel", "Start a model's default instance", "Models", true, false, "202"},
		{http.MethodPost, "/api/v1/models/{id}/stop", "stopModel", "Stop all instances for a model", "Models", true, false, "204"},
		{http.MethodGet, "/api/v1/models/{id}/runtime", "getModelRuntime", "Get model runtime state", "Models", true, false, "200"},
		{http.MethodPost, "/api/v1/models/inspect", "inspectModel", "Inspect GGUF model metadata", "Models", true, true, "200"},
		{http.MethodGet, "/api/v1/models/{id}/details", "getModelDetails", "Get model GGUF metadata", "Models", true, false, "200"},
		{http.MethodGet, "/api/v1/models/{id}/details/value", "getModelMetadataValue", "Get one model metadata value", "Models", true, false, "200"},
		{http.MethodGet, "/api/v1/models/{id}/recommendation", "getModelRecommendation", "Get hardware configuration recommendation", "Models", true, false, "200"},
		{http.MethodGet, "/api/v1/instances", "listInstances", "List configured instances", "Instances", true, false, "200"},
		{http.MethodPost, "/api/v1/instances", "createInstance", "Create an instance", "Instances", true, true, "201"},
		{http.MethodGet, "/api/v1/instances/{id}", "getInstance", "Get an instance", "Instances", true, false, "200"},
		{http.MethodPut, "/api/v1/instances/{id}", "updateInstance", "Update an instance", "Instances", true, true, "200"},
		{http.MethodDelete, "/api/v1/instances/{id}", "deleteInstance", "Delete an instance", "Instances", true, false, "204"},
		{http.MethodPost, "/api/v1/instances/{id}/start", "startInstance", "Start an instance", "Instances", true, false, "202"},
		{http.MethodPost, "/api/v1/instances/{id}/stop", "stopInstance", "Stop an instance", "Instances", true, false, "204"},
		{http.MethodPost, "/api/v1/instances/{id}/restart", "restartInstance", "Restart an instance", "Instances", true, false, "202"},
		{http.MethodPost, "/api/v1/instances/{id}/kill", "killInstance", "Kill an instance", "Instances", true, false, "204"},
		{http.MethodPost, "/api/v1/instances/{id}/duplicate", "duplicateInstance", "Duplicate an instance", "Instances", true, false, "201"},
		{http.MethodGet, "/api/v1/instances/{id}/runtime", "getInstanceRuntime", "Get instance runtime state", "Instances", true, false, "200"},
		{http.MethodGet, "/api/v1/instances/{id}/options", "getInstanceOptions", "Get instance llama.cpp options", "Instances", true, false, "200"},
		{http.MethodGet, "/api/v1/instances/{id}/logs", "getInstanceLogs", "Get instance log snapshot", "Logs", true, false, "200"},
		{http.MethodGet, "/api/v1/instances/{id}/logs/stream", "streamInstanceLogs", "Stream instance logs with server-sent events", "Logs", true, false, "200"},
		{http.MethodGet, "/api/v1/logs", "listLogs", "List manager logs", "Logs", true, false, "200"},
		{http.MethodGet, "/api/v1/hardware", "getHardware", "Get detected hardware", "Hardware", true, false, "200"},
		{http.MethodGet, "/api/v1/llamacpp/profile", "getLlamaCppProfile", "Get discovered llama.cpp binary capabilities", "llama.cpp", true, false, "200"},
		{http.MethodGet, "/api/v1/llamacpp/config", "getLlamaCppConfig", "Get global llama.cpp defaults", "llama.cpp", true, false, "200"},
		{http.MethodPut, "/api/v1/llamacpp/config", "updateLlamaCppConfig", "Update global llama.cpp defaults", "llama.cpp", true, true, "200"},
		{http.MethodGet, "/api/v1/huggingface/search", "searchHuggingFace", "Search Hugging Face models", "Hugging Face", true, false, "200"},
		{http.MethodGet, "/api/v1/huggingface/model", "getHuggingFaceModel", "Get Hugging Face model details", "Hugging Face", true, false, "200"},
		{http.MethodGet, "/api/v1/huggingface/token", "getHuggingFaceTokenStatus", "Get Hugging Face token status", "Hugging Face", true, false, "200"},
		{http.MethodPut, "/api/v1/huggingface/token", "setHuggingFaceToken", "Set the Hugging Face token", "Hugging Face", true, true, "200"},
		{http.MethodDelete, "/api/v1/huggingface/token", "deleteHuggingFaceToken", "Remove the Hugging Face token", "Hugging Face", true, false, "204"},
		{http.MethodPost, "/api/v1/huggingface/import", "importHuggingFaceModel", "Prepare a Hugging Face model import", "Hugging Face", true, true, "201"},
		{http.MethodGet, "/api/v1/imports", "listImports", "List provider imports", "Downloads", true, false, "200"},
		{http.MethodGet, "/api/v1/downloads", "listDownloads", "List download jobs", "Downloads", true, false, "200"},
		{http.MethodGet, "/api/v1/downloads/ws", "streamDownloadEvents", "Stream download events over WebSocket", "Downloads", true, false, "101"},
		{http.MethodGet, "/api/v1/observability/summary", "getObservabilitySummary", "Get observability summary", "Observability", true, false, "200"},
		{http.MethodGet, "/api/v1/observability/requests", "listObservabilityRequests", "List inference request history", "Observability", true, false, "200"},
		{http.MethodGet, "/api/v1/observability/requests/{request_id}", "getObservabilityRequestByID", "Get an inference request by manager request ID", "Observability", true, false, "200"},
		{http.MethodGet, "/api/v1/observability/playground/{request_id}", "getPlaygroundDiagnostics", "Get correlated Playground request diagnostics", "Observability", true, false, "200"},
		{http.MethodGet, "/api/v1/observability/timeseries", "getObservabilityTimeseries", "Get observability timeseries data", "Observability", true, false, "200"},
		{http.MethodGet, "/api/v1/ws", "streamRuntimeEvents", "Stream runtime events over WebSocket", "Observability", true, false, "101"},
	}

	for _, route := range routes {
		responses := map[string]manageropenapi.Response{}
		if route.response == "204" {
			responses[route.response] = manageropenapi.EmptyResponse("Success")
		} else if route.response == "101" {
			responses[route.response] = manageropenapi.EmptyResponse("Protocol switched to WebSocket")
		} else {
			responses[route.response] = manageropenapi.JSONResponse("Success", manageropenapi.ObjectSchema())
		}
		responses["400"] = manageropenapi.ErrorResponse("Invalid request")
		responses["404"] = manageropenapi.ErrorResponse("Not found")
		responses["500"] = manageropenapi.ErrorResponse("Internal server error")
		op := manageropenapi.Operation{
			OperationID: route.operationID,
			Summary:     route.summary,
			Tags:        []string{route.tag},
			Responses:   responses,
		}
		if route.security {
			op.Security = []map[string][]string{{"managerSession": {}}}
			responses["401"] = manageropenapi.ErrorResponse("Authentication required")
		}
		if route.requestBody {
			op.RequestBody = manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true)
		}
		if containsPathParameter(route.path, "id") {
			op.Parameters = append(op.Parameters, pathParameter("id", "Resource identifier"))
		}
		if containsPathParameter(route.path, "request_id") {
			op.Parameters = append(op.Parameters, pathParameter("request_id", "Stable x-llamacpp-manager-request-id correlation identifier"))
		}
		if route.path == "/api/v1/instances/{id}/logs/stream" {
			op.Description = "Server-sent event stream. OpenAPI describes the handshake; the response remains streaming and is flushed incrementally."
			responses["200"] = manageropenapi.Response{Description: "SSE log stream", Content: map[string]manageropenapi.MediaType{"text/event-stream": {Schema: manageropenapi.Schema{Type: "string"}}}}
		}
		if route.response == "101" {
			op.Description = "OpenAPI describes the HTTP upgrade handshake. Message framing after the WebSocket upgrade is protocol-specific."
		}
		doc.MustRegister(route.method, route.path, op)
	}
}

func registerInferenceOperations(doc *manageropenapi.Document) {
	doc.MustRegister(http.MethodGet, "/v1/models", manageropenapi.Operation{
		OperationID: "listOpenAIModels",
		Summary:     "List addressable OpenAI-compatible model IDs",
		Tags:        []string{"OpenAI Compatible"},
		Security:    []map[string][]string{{"bearerAPIKey": {}}},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("OpenAI-compatible model list", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
		},
	})
	for _, endpoint := range []struct {
		path, id, summary string
		embeddings        bool
	}{
		{"/v1/chat/completions", "createChatCompletion", "Create a chat completion", false},
		{"/v1/completions", "createCompletion", "Create a completion", false},
		{"/v1/responses", "createResponse", "Create a response", false},
		{"/v1/embeddings", "createEmbedding", "Create embeddings", true},
	} {
		headers := managerMetricHeaders(endpoint.embeddings)
		doc.MustRegister(http.MethodPost, endpoint.path, manageropenapi.Operation{
			OperationID: endpoint.id,
			Summary:     endpoint.summary,
			Description: "The JSON/SSE body remains OpenAI-compatible. LlamaCPP Manager observability is exposed only through x-llamacpp-manager-* response headers. For streaming responses only metrics known before headers are committed are returned; final metrics remain queryable through /api/v1/observability/requests/{request_id}.",
			Tags:        []string{"OpenAI Compatible"},
			Security:    []map[string][]string{{"bearerAPIKey": {}}},
			RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
			Responses: map[string]manageropenapi.Response{
				"200": {Description: "OpenAI-compatible response", Headers: headers, Content: map[string]manageropenapi.MediaType{
					"application/json":  {Schema: manageropenapi.ObjectSchema()},
					"text/event-stream": {Schema: manageropenapi.Schema{Type: "string"}},
				}},
				"400": manageropenapi.ErrorResponse("Invalid request"),
				"401": manageropenapi.ErrorResponse("Invalid API key"),
				"503": {Description: "Model or worker unavailable", Headers: preResponseMetricHeaders(), Content: map[string]manageropenapi.MediaType{"application/json": {Schema: manageropenapi.Schema{Ref: "#/components/schemas/Error"}}}},
			},
		})
	}
}

func managerMetricHeaders(embeddings bool) map[string]manageropenapi.Header {
	headers := preResponseMetricHeaders()
	headers["x-llamacpp-manager-ttft-ms"] = numberHeader("Milliseconds from manager request start until the first upstream response byte. Omitted when not known before headers are committed.")
	headers["x-llamacpp-manager-prompt-tokens-per-second"] = numberHeader("Prompt-processing throughput reported or derived from llama.cpp timings, in tokens per second.")
	headers["x-llamacpp-manager-prompt-tokens"] = integerHeader("Final prompt token count when known.")
	headers["x-llamacpp-manager-total-tokens"] = integerHeader("Final total token count when known.")
	if !embeddings {
		headers["x-llamacpp-manager-generation-tokens-per-second"] = numberHeader("Generation throughput reported or derived from llama.cpp timings, in tokens per second.")
		headers["x-llamacpp-manager-generated-tokens"] = integerHeader("Final generated token count when known.")
	}
	return headers
}

func preResponseMetricHeaders() map[string]manageropenapi.Header {
	return map[string]manageropenapi.Header{
		"x-llamacpp-manager-request-id":    {Description: "Stable, non-secret manager correlation ID. The same ID identifies the persisted observability request record.", Schema: manageropenapi.Schema{Type: "string"}},
		"x-llamacpp-manager-instance":      {Description: "Selected addressable Instance ID.", Schema: manageropenapi.Schema{Type: "string"}},
		"x-llamacpp-manager-autoloaded":    {Description: "Whether this request had to load/start the selected Instance.", Schema: manageropenapi.Schema{Type: "boolean"}},
		"x-llamacpp-manager-upstream-port": {Description: "Resolved internal llama.cpp worker port. This is diagnostic metadata only; clients must continue to use the manager gateway.", Schema: manageropenapi.Schema{Type: "integer", Format: "int64"}},
		"x-llamacpp-manager-queue-ms":      numberHeader("Time spent waiting for Instance acquisition, in milliseconds."),
		"x-llamacpp-manager-load-ms":       numberHeader("Autoload/model-load time in milliseconds. Omitted when no load occurred."),
	}
}

func numberHeader(description string) manageropenapi.Header {
	return manageropenapi.Header{Description: description, Schema: manageropenapi.Schema{Type: "number", Format: "double"}}
}

func integerHeader(description string) manageropenapi.Header {
	return manageropenapi.Header{Description: description, Schema: manageropenapi.Schema{Type: "integer", Format: "int64"}}
}

func pathParameter(name, description string) manageropenapi.Parameter {
	return manageropenapi.Parameter{Name: name, In: "path", Required: true, Description: description, Schema: manageropenapi.Schema{Type: "string"}}
}

func containsPathParameter(path, name string) bool {
	return len(path) > 0 && len(name) > 0 && stringContains(path, "{"+name+"}")
}

func stringContains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
