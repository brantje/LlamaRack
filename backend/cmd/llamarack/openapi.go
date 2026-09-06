package main

import (
	"net/http"
	"runtime/debug"
	"strings"

	manageropenapi "github.com/brantje/llamarack/backend/internal/openapi"
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
		{http.MethodDelete, "/api/v1/me/sessions/{id}", "revokeCurrentUserSession", "Revoke one of the current user's sessions", "Profile", true, false, "204"},
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
		{http.MethodGet, "/api/v1/api-keys", "listAPIKeys", "List API keys", "API Keys", true, false, "200"},
		{http.MethodPost, "/api/v1/api-keys", "createAPIKey", "Create an API key", "API Keys", true, true, "201"},
		{http.MethodPatch, "/api/v1/api-keys/{id}", "updateAPIKey", "Update an API key", "API Keys", true, true, "204"},
		{http.MethodPost, "/api/v1/api-keys/{id}/rotate", "rotateAPIKey", "Rotate an API key secret in place", "API Keys", true, false, "200"},
		{http.MethodGet, "/api/v1/admin/service-accounts", "listServiceAccounts", "List service accounts (JWT or Full Access key)", "Service Accounts", true, false, "200"},
		{http.MethodPost, "/api/v1/admin/service-accounts", "createServiceAccount", "Create a service account (JWT or Full Access key)", "Service Accounts", true, true, "201"},
		{http.MethodGet, "/api/v1/admin/service-accounts/{id}", "getServiceAccount", "Get a service account and its keys (JWT or Full Access key)", "Service Accounts", true, false, "200"},
		{http.MethodPatch, "/api/v1/admin/service-accounts/{id}", "updateServiceAccount", "Update a service account (JWT or Full Access key)", "Service Accounts", true, true, "204"},
		{http.MethodDelete, "/api/v1/admin/service-accounts/{id}", "deleteServiceAccount", "Delete a service account and its keys (JWT or Full Access key)", "Service Accounts", true, false, "204"},
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
		{http.MethodGet, "/api/v1/litellm", "getLiteLLMStatus", "Get LiteLLM integration status", "LiteLLM", true, false, "200"},
		{http.MethodPut, "/api/v1/litellm", "saveLiteLLMSettings", "Save LiteLLM integration settings", "LiteLLM", true, true, "200"},
		{http.MethodDelete, "/api/v1/litellm", "disconnectLiteLLM", "Disconnect LiteLLM integration", "LiteLLM", true, true, "204"},
		{http.MethodPost, "/api/v1/litellm/test", "testLiteLLMConnection", "Test LiteLLM proxy connection", "LiteLLM", true, false, "200"},
		{http.MethodPost, "/api/v1/litellm/sync", "syncLiteLLMCatalog", "Sync enabled instances to LiteLLM", "LiteLLM", true, false, "200"},
		{http.MethodPost, "/api/v1/litellm/rotate", "rotateLiteLLMInferenceKey", "Rotate the managed LiteLLM inference key", "LiteLLM", true, false, "200"},
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
			op.Security = []map[string][]string{{"managementBearer": {}}}
			responses["401"] = manageropenapi.ErrorResponse("Authentication required")
			responses["403"] = manageropenapi.ErrorResponse("This credential cannot access this endpoint")
		}
		if route.requestBody {
			op.RequestBody = manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true)
		}
		if containsPathParameter(route.path, "id") {
			op.Parameters = append(op.Parameters, pathParameter("id", "Resource identifier"))
		}
		if containsPathParameter(route.path, "request_id") {
			op.Parameters = append(op.Parameters, pathParameter("request_id", "Stable X-LlamaRack-Request-ID correlation identifier"))
		}
		if strings.HasPrefix(route.path, "/api/v1/admin/service-accounts") {
			op.Description = "Service-account administration is allowed for a management JWT or a Full Access API key (any owner, including service-account-owned). Management and inference keys receive 403."
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
	const persistenceNote = "Manager-side retrievability is governed by the selected Instance request_log_mode (full retains request/response bodies; metadata does not). The OpenAI store field is forwarded to llama.cpp unchanged and does not override Manager persistence. DELETE /v1/responses/{response_id} only hides the Response from the OpenAI-compatible API; /logs and observability retain the original row. Normal observability retention is the maximum lifetime of a retrievable Response. previous_response_id is forwarded to llama.cpp but Manager does not reconstruct prior turns from stored history."
	bearer := []map[string][]string{{"bearerAPIKey": {}}}
	doc.MustRegister(http.MethodGet, "/v1/models", manageropenapi.Operation{
		OperationID: "listOpenAIModels",
		Summary:     "List addressable OpenAI-compatible model IDs",
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("OpenAI-compatible model list", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
		},
	})
	doc.MustRegister(http.MethodGet, "/v1/models/{model}", manageropenapi.Operation{
		OperationID: "retrieveOpenAIModel",
		Summary:     "Retrieve one addressable OpenAI-compatible model ID",
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		Parameters:  []manageropenapi.Parameter{pathParameter("model", "Addressable Instance ID")},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("OpenAI-compatible model object", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"404": manageropenapi.ErrorResponse("Unknown or disabled model"),
		},
	})
	for _, endpoint := range []struct {
		path, id, summary string
		embeddings        bool
	}{
		{"/v1/chat/completions", "createChatCompletion", "Create a chat completion", false},
		{"/v1/completions", "createCompletion", "Create a completion", false},
		{"/v1/embeddings", "createEmbedding", "Create embeddings", true},
	} {
		headers := managerMetricHeaders(endpoint.embeddings)
		doc.MustRegister(http.MethodPost, endpoint.path, manageropenapi.Operation{
			OperationID: endpoint.id,
			Summary:     endpoint.summary,
			Description: "The JSON/SSE body remains OpenAI-compatible. LlamaRack observability is exposed only through X-LlamaRack-* response headers. For streaming responses only metrics known before headers are committed are returned; final metrics remain queryable through /api/v1/observability/requests/{request_id}.",
			Tags:        []string{"OpenAI Compatible"},
			Security:    bearer,
			RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
			Responses: map[string]manageropenapi.Response{
				"200": {Description: "OpenAI-compatible response", Headers: headers, Content: map[string]manageropenapi.MediaType{
					"application/json":  {Schema: manageropenapi.ObjectSchema()},
					"text/event-stream": {Schema: manageropenapi.Schema{Type: "string"}},
				}},
				"400": manageropenapi.ErrorResponse("Invalid request"),
				"401": manageropenapi.ErrorResponse("Invalid API key"),
				"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
				"503": {Description: "Model or worker unavailable, or pending-request admission limit exceeded", Headers: preResponseMetricHeaders(), Content: map[string]manageropenapi.MediaType{"application/json": {Schema: manageropenapi.Schema{Ref: "#/components/schemas/Error"}}}},
			},
		})
	}
	doc.MustRegister(http.MethodPost, "/v1/responses", manageropenapi.Operation{
		OperationID: "createResponse",
		Summary:     "Create a response",
		Description: "The JSON/SSE body remains OpenAI-compatible. " + persistenceNote,
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
		Responses: map[string]manageropenapi.Response{
			"200": {Description: "OpenAI-compatible response", Headers: managerMetricHeaders(false), Content: map[string]manageropenapi.MediaType{
				"application/json":  {Schema: manageropenapi.ObjectSchema()},
				"text/event-stream": {Schema: manageropenapi.Schema{Type: "string"}},
			}},
			"400": manageropenapi.ErrorResponse("Invalid request"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"503": {Description: "Model or worker unavailable, or pending-request admission limit exceeded", Headers: preResponseMetricHeaders(), Content: map[string]manageropenapi.MediaType{"application/json": {Schema: manageropenapi.Schema{Ref: "#/components/schemas/Error"}}}},
		},
	})
	doc.MustRegister(http.MethodGet, "/v1/responses/{response_id}", manageropenapi.Operation{
		OperationID: "getResponse",
		Summary:     "Retrieve a stored Response",
		Description: persistenceNote,
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		Parameters:  []manageropenapi.Parameter{pathParameter("response_id", "Upstream OpenAI-compatible Response ID")},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Stored Response JSON", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"404": manageropenapi.ErrorResponse("Response not found"),
		},
	})
	doc.MustRegister(http.MethodDelete, "/v1/responses/{response_id}", manageropenapi.Operation{
		OperationID: "deleteResponse",
		Summary:     "Hide a stored Response from the OpenAI-compatible API",
		Description: persistenceNote,
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		Parameters:  []manageropenapi.Parameter{pathParameter("response_id", "Upstream OpenAI-compatible Response ID")},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Deletion acknowledgement", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"404": manageropenapi.ErrorResponse("Response not found"),
		},
	})
	doc.MustRegister(http.MethodGet, "/v1/responses/{response_id}/input_items", manageropenapi.Operation{
		OperationID: "listResponseInputItems",
		Summary:     "List retained input items for a stored Response",
		Description: persistenceNote,
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		Parameters: []manageropenapi.Parameter{
			pathParameter("response_id", "Upstream OpenAI-compatible Response ID"),
			{Name: "limit", In: "query", Description: "Maximum number of items to return. Defaults to 20 and is capped at 100.", Schema: manageropenapi.Schema{Type: "integer", Format: "int64"}},
			{Name: "after", In: "query", Description: "Return items after this item ID.", Schema: manageropenapi.Schema{Type: "string"}},
		},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Input item list", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"404": manageropenapi.ErrorResponse("Response not found"),
		},
	})
	doc.MustRegister(http.MethodPost, "/v1/responses/{response_id}/cancel", manageropenapi.Operation{
		OperationID: "cancelResponse",
		Summary:     "Cancel an in-flight Response",
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		Parameters:  []manageropenapi.Parameter{pathParameter("response_id", "Upstream OpenAI-compatible Response ID")},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Cancelled or current Response", manageropenapi.ObjectSchema()),
			"400": manageropenapi.ErrorResponse("Response is not cancellable"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"404": manageropenapi.ErrorResponse("Response not found"),
		},
	})
	doc.MustRegister(http.MethodPost, "/v1/responses/input_tokens", manageropenapi.Operation{
		OperationID: "countResponseInputTokens",
		Summary:     "Count Responses input tokens",
		Description: "Proxied to llama.cpp. If the worker does not implement this route, Manager returns 501.",
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
		Responses: map[string]manageropenapi.Response{
			"200": {Description: "Token count", Headers: managerMetricHeaders(true), Content: map[string]manageropenapi.MediaType{"application/json": {Schema: manageropenapi.ObjectSchema()}}},
			"400": manageropenapi.ErrorResponse("Invalid request"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"501": manageropenapi.ErrorResponse("Worker does not implement this route"),
			"503": manageropenapi.ErrorResponse("Model or worker unavailable, or pending-request admission limit exceeded"),
		},
	})
	doc.MustRegister(http.MethodPost, "/v1/audio/transcriptions", manageropenapi.Operation{
		OperationID: "createTranscription",
		Summary:     "Create an audio transcription",
		Description: "Multipart form request. The model field is the addressable Instance ID. Binary audio is not stored as TEXT; full request logging retains filename, content type, and size.",
		Tags:        []string{"OpenAI Compatible"},
		Security:    bearer,
		RequestBody: manageropenapi.MultipartBody(true),
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Transcription", manageropenapi.ObjectSchema()),
			"400": manageropenapi.ErrorResponse("Invalid request"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"413": manageropenapi.ErrorResponse("Request body is too large"),
			"503": manageropenapi.ErrorResponse("Model or worker unavailable, or pending-request admission limit exceeded"),
		},
	})
	doc.MustRegister(http.MethodPost, "/v1/chat/completions/input_tokens", manageropenapi.Operation{
		OperationID: "countChatCompletionInputTokens",
		Summary:     "Count chat completion input tokens",
		Description: "llama.cpp extension. Proxied to the selected Instance. If the worker does not implement this route, Manager returns 501.",
		Tags:        []string{"llama.cpp Extensions"},
		Security:    bearer,
		RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
		Responses: map[string]manageropenapi.Response{
			"200": {Description: "Token count", Headers: managerMetricHeaders(true), Content: map[string]manageropenapi.MediaType{"application/json": {Schema: manageropenapi.ObjectSchema()}}},
			"400": manageropenapi.ErrorResponse("Invalid request"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"501": manageropenapi.ErrorResponse("Worker does not implement this route"),
			"503": manageropenapi.ErrorResponse("Model or worker unavailable, or pending-request admission limit exceeded"),
		},
	})
	doc.MustRegister(http.MethodPost, "/v1/chat/completions/control", manageropenapi.Operation{
		OperationID: "controlChatCompletion",
		Summary:     "Control an in-flight chat completion",
		Description: "llama.cpp extension. Routes the control payload to the worker that owns the in-flight completion ID.",
		Tags:        []string{"llama.cpp Extensions"},
		Security:    bearer,
		RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Control result", manageropenapi.ObjectSchema()),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"404": manageropenapi.ErrorResponse("Unknown in-flight completion"),
			"503": manageropenapi.ErrorResponse("Model or worker unavailable, or pending-request admission limit exceeded"),
		},
	})
	for _, endpoint := range []struct{ path, id, summary string }{
		{"/v1/rerank", "createRerank", "Rerank documents"},
		{"/v1/reranking", "createReranking", "Rerank documents (alias)"},
	} {
		doc.MustRegister(http.MethodPost, endpoint.path, manageropenapi.Operation{
			OperationID: endpoint.id,
			Summary:     endpoint.summary,
			Description: "llama.cpp extension. Proxied with the same auth, lifecycle, and observability behavior as other inference routes.",
			Tags:        []string{"llama.cpp Extensions"},
			Security:    bearer,
			RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true),
			Responses: map[string]manageropenapi.Response{
				"200": {Description: "Rerank result", Headers: managerMetricHeaders(true), Content: map[string]manageropenapi.MediaType{"application/json": {Schema: manageropenapi.ObjectSchema()}}},
				"400": manageropenapi.ErrorResponse("Invalid request"),
				"401": manageropenapi.ErrorResponse("Invalid API key"),
				"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
				"503": manageropenapi.ErrorResponse("Model or worker unavailable, or pending-request admission limit exceeded"),
			},
		})
	}
	doc.MustRegister(http.MethodGet, "/v1/slots", manageropenapi.Operation{
		OperationID: "listSlots",
		Summary:     "List llama.cpp slots",
		Description: "llama.cpp extension. Proxied to the selected READY Instance using the model query parameter (Instance ID). Does not autoload stopped Instances. GET /slots may include in-flight prompts from other concurrent traffic on the same worker. If the worker does not implement this route, Manager returns 501.",
		Tags:        []string{"llama.cpp Extensions"},
		Security:    bearer,
		Parameters: []manageropenapi.Parameter{
			{Name: "model", In: "query", Description: "Addressable Instance ID.", Required: true, Schema: manageropenapi.Schema{Type: "string"}},
		},
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Slot list", manageropenapi.ObjectSchema()),
			"400": manageropenapi.ErrorResponse("Invalid request"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"501": manageropenapi.ErrorResponse("Worker does not implement this route"),
			"503": manageropenapi.ErrorResponse("Instance is not READY and slots polling does not autoload"),
		},
	})
	doc.MustRegister(http.MethodPost, "/v1/slots/{slot_id}", manageropenapi.Operation{
		OperationID: "slotsAction",
		Summary:     "Save, restore, or erase a llama.cpp slot",
		Description: "llama.cpp extension. Proxied to POST /slots/{slot_id}?action=save|restore|erase on the selected READY Instance. The model query parameter is the Instance ID. save and restore require a JSON filename; erase accepts an empty body. Manager rejects path-escaping filenames. Does not autoload stopped Instances.",
		Tags:        []string{"llama.cpp Extensions"},
		Security:    bearer,
		Parameters: []manageropenapi.Parameter{
			{Name: "slot_id", In: "path", Description: "Slot identifier.", Required: true, Schema: manageropenapi.Schema{Type: "string"}},
			{Name: "model", In: "query", Description: "Addressable Instance ID.", Required: true, Schema: manageropenapi.Schema{Type: "string"}},
			{Name: "action", In: "query", Description: "Slot action.", Required: true, Schema: manageropenapi.Schema{Type: "string", Enum: []string{"save", "restore", "erase"}}},
		},
		RequestBody: manageropenapi.JSONBody(manageropenapi.ObjectSchema(), false),
		Responses: map[string]manageropenapi.Response{
			"200": manageropenapi.JSONResponse("Slot action result", manageropenapi.ObjectSchema()),
			"400": manageropenapi.ErrorResponse("Invalid request"),
			"401": manageropenapi.ErrorResponse("Invalid API key"),
			"403": manageropenapi.ErrorResponse("API key cannot access this inference route or instance"),
			"501": manageropenapi.ErrorResponse("Worker does not implement this route"),
			"503": manageropenapi.ErrorResponse("Instance is not READY and slots polling does not autoload"),
		},
	})
}

func managerMetricHeaders(embeddings bool) map[string]manageropenapi.Header {
	headers := preResponseMetricHeaders()
	headers["x-llamarack-ttft-ms"] = numberHeader("Milliseconds from manager request start until the first upstream response byte. Omitted when not known before headers are committed.")
	headers["x-llamarack-prompt-tokens-per-second"] = numberHeader("Prompt-processing throughput reported or derived from llama.cpp timings, in tokens per second.")
	headers["x-llamarack-prompt-tokens"] = integerHeader("Final prompt token count when known.")
	headers["x-llamarack-total-tokens"] = integerHeader("Final total token count when known.")
	if !embeddings {
		headers["x-llamarack-generation-tokens-per-second"] = numberHeader("Generation throughput reported or derived from llama.cpp timings, in tokens per second.")
		headers["x-llamarack-generated-tokens"] = integerHeader("Final generated token count when known.")
	}
	return headers
}

func preResponseMetricHeaders() map[string]manageropenapi.Header {
	return map[string]manageropenapi.Header{
		"x-llamarack-request-id": {Description: "Stable, non-secret manager correlation ID. The same ID identifies the persisted observability request record.", Schema: manageropenapi.Schema{Type: "string"}},
		"x-llamarack-instance":   {Description: "Selected addressable Instance ID.", Schema: manageropenapi.Schema{Type: "string"}},
		"x-llamarack-autoloaded": {Description: "Whether this request had to load/start the selected Instance.", Schema: manageropenapi.Schema{Type: "boolean"}},
		"x-llamarack-queue-ms":   numberHeader("Time spent waiting for Instance acquisition, in milliseconds."),
		"x-llamarack-load-ms":    numberHeader("Autoload/model-load time in milliseconds. Omitted when no load occurred."),
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
