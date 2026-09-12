package main

import (
	"net/http"

	manageropenapi "github.com/brantje/llamarack/backend/internal/openapi"
)

func registerBenchmarkOpenAPIOperations(doc *manageropenapi.Document) {
	management := []map[string][]string{{"managementBearer": {}}}
	commonErrors := func() map[string]manageropenapi.Response {
		return map[string]manageropenapi.Response{
			"400": manageropenapi.ErrorResponse("Invalid benchmark request"),
			"401": manageropenapi.ErrorResponse("Authentication required"),
			"403": manageropenapi.ErrorResponse("This credential cannot access this endpoint"),
			"404": manageropenapi.ErrorResponse("Benchmark or Instance not found"),
			"409": manageropenapi.ErrorResponse("Benchmark state or resource admission conflict"),
			"500": manageropenapi.ErrorResponse("Internal server error"),
			"503": manageropenapi.ErrorResponse("llama-bench or required hardware discovery is unavailable"),
		}
	}
	register := func(method, path, id, summary string, requestBody bool, success string) {
		responses := commonErrors()
		if success == "204" {
			responses[success] = manageropenapi.EmptyResponse("Benchmark deleted")
		} else {
			responses[success] = manageropenapi.JSONResponse("Benchmark response", manageropenapi.ObjectSchema())
		}
		op := manageropenapi.Operation{OperationID: id, Summary: summary, Tags: []string{"Benchmarks"}, Security: management, Responses: responses}
		if requestBody {
			op.RequestBody = manageropenapi.JSONBody(manageropenapi.ObjectSchema(), true)
		}
		if path == "/api/v1/instances/{id}/benchmarks" {
			op.Parameters = []manageropenapi.Parameter{pathParameter("id", "Saved Instance slug or durable ID")}
			op.Description = "Starts llama-bench from an immutable server-resolved snapshot of the saved Instance. The request may provide a workload and safe structured runtime_overrides advertised by the benchmark capabilities endpoint. Executable paths, model paths, helper paths, raw argv and arbitrary filesystem references are not accepted. Overrides are run-scoped and never mutate the saved Instance. Admission is non-preemptive and uses the effective benchmark configuration."
		} else if path == "/api/v1/benchmarks/{id}" || path == "/api/v1/benchmarks/{id}/cancel" {
			op.Parameters = []manageropenapi.Parameter{pathParameter("id", "Benchmark run identifier")}
		} else if path == "/api/v1/benchmarks" {
			op.Parameters = []manageropenapi.Parameter{{Name: "instance_id", In: "query", Description: "Filter history to a saved Instance ID.", Schema: manageropenapi.Schema{Type: "string"}}, {Name: "model_id", In: "query", Description: "Filter history to a saved Model ID.", Schema: manageropenapi.Schema{Type: "string"}}, {Name: "status", In: "query", Description: "Filter history to a benchmark run status.", Schema: manageropenapi.Schema{Type: "string", Enum: []string{"QUEUED", "RUNNING", "COMPLETED", "FAILED", "CANCELLED"}}}, {Name: "limit", In: "query", Description: "Maximum number of items to return. Defaults to 50 and is capped at 100.", Schema: manageropenapi.Schema{Type: "integer", Format: "int64"}}, {Name: "offset", In: "query", Description: "Number of items to skip before returning results.", Schema: manageropenapi.Schema{Type: "integer", Format: "int64"}}}
		}
		doc.MustRegister(method, path, op)
	}
	register(http.MethodGet, "/api/v1/benchmarks", "listBenchmarks", "List benchmark history", false, "200")
	register(http.MethodGet, "/api/v1/benchmarks/capabilities", "getBenchmarkCapabilities", "Get llama-bench runtime and workload capabilities", false, "200")
	register(http.MethodPost, "/api/v1/instances/{id}/benchmarks", "createInstanceBenchmark", "Run a benchmark from a saved Instance", true, "201")
	register(http.MethodGet, "/api/v1/benchmarks/{id}", "getBenchmark", "Get a benchmark run and results", false, "200")
	register(http.MethodPost, "/api/v1/benchmarks/{id}/cancel", "cancelBenchmark", "Cancel a queued or running benchmark", false, "202")
	register(http.MethodDelete, "/api/v1/benchmarks/{id}", "deleteBenchmark", "Delete a terminal benchmark run", false, "204")
}
