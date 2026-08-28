package openapi

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const Version = "3.1.0"

type Document struct {
	OpenAPI    string                          `json:"openapi"`
	Info       Info                            `json:"info"`
	Paths      map[string]map[string]Operation `json:"paths"`
	Components Components                      `json:"components,omitempty"`

	mu sync.RWMutex `json:"-"`
}

type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type Components struct {
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
	Schemas         map[string]Schema         `json:"schemas,omitempty"`
}

type SecurityScheme struct {
	Type   string `json:"type"`
	Scheme string `json:"scheme,omitempty"`
	In     string `json:"in,omitempty"`
	Name   string `json:"name,omitempty"`
}

type Operation struct {
	OperationID string                `json:"operationId"`
	Summary     string                `json:"summary"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Parameters  []Parameter           `json:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses"`
	Security    []map[string][]string `json:"security,omitempty"`
}

type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Schema      Schema `json:"schema"`
}

type RequestBody struct {
	Description string               `json:"description,omitempty"`
	Required    bool                 `json:"required,omitempty"`
	Content     map[string]MediaType `json:"content"`
}

type Response struct {
	Description string               `json:"description"`
	Headers     map[string]Header    `json:"headers,omitempty"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

type Header struct {
	Description string `json:"description,omitempty"`
	Schema      Schema `json:"schema"`
}

type MediaType struct {
	Schema Schema `json:"schema,omitempty"`
}

type Schema struct {
	Type                 string            `json:"type,omitempty"`
	Format               string            `json:"format,omitempty"`
	Description          string            `json:"description,omitempty"`
	Ref                  string            `json:"$ref,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	Required             []string          `json:"required,omitempty"`
	Enum                 []string          `json:"enum,omitempty"`
	AdditionalProperties any               `json:"additionalProperties,omitempty"`
	Minimum              *float64          `json:"minimum,omitempty"`
	Maximum              *float64          `json:"maximum,omitempty"`
	Default              any               `json:"default,omitempty"`
	Examples             []any             `json:"examples,omitempty"`
}

func New(applicationVersion string) *Document {
	if strings.TrimSpace(applicationVersion) == "" {
		applicationVersion = "development"
	}
	return &Document{
		OpenAPI: Version,
		Info: Info{
			Title:       "LlamaCPP Manager API",
			Version:     applicationVersion,
			Description: "Runtime-generated management and OpenAI-compatible API contract for LlamaCPP Manager.",
		},
		Paths: map[string]map[string]Operation{},
		Components: Components{
			SecuritySchemes: map[string]SecurityScheme{
				"managerSession": {Type: "apiKey", In: "cookie", Name: "llamacpp_manager_session"},
				"bearerAPIKey":   {Type: "http", Scheme: "bearer"},
			},
			Schemas: map[string]Schema{
				"Error": {
					Type: "object",
					Properties: map[string]Schema{
						"error": {Type: "object", AdditionalProperties: true},
					},
					Required: []string{"error"},
				},
			},
		},
	}
}

func (d *Document) Register(method, path string, operation Operation) error {
	method = strings.ToLower(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if method == "" || path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("openapi operation requires method and absolute path")
	}
	if operation.OperationID == "" {
		return fmt.Errorf("openapi operation %s %s requires operationId", strings.ToUpper(method), path)
	}
	if operation.Summary == "" {
		return fmt.Errorf("openapi operation %s requires summary", operation.OperationID)
	}
	if len(operation.Responses) == 0 {
		return fmt.Errorf("openapi operation %s requires responses", operation.OperationID)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for registeredPath, methods := range d.Paths {
		for registeredMethod, existing := range methods {
			if existing.OperationID == operation.OperationID {
				return fmt.Errorf("duplicate operationId %q already used by %s %s", operation.OperationID, strings.ToUpper(registeredMethod), registeredPath)
			}
		}
	}
	if d.Paths[path] == nil {
		d.Paths[path] = map[string]Operation{}
	}
	if _, exists := d.Paths[path][method]; exists {
		return fmt.Errorf("duplicate openapi operation %s %s", strings.ToUpper(method), path)
	}
	d.Paths[path][method] = operation
	return nil
}

func (d *Document) MustRegister(method, path string, operation Operation) {
	if err := d.Register(method, path, operation); err != nil {
		panic(err)
	}
}

func (d *Document) HasOperation(method, path string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.Paths[path][strings.ToLower(method)]
	return ok
}

func (d *Document) OperationIDs() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ids := make([]string, 0)
	for _, methods := range d.Paths {
		for _, operation := range methods {
			ids = append(ids, operation.OperationID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (d *Document) JSONHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		d.mu.RLock()
		payload, err := json.Marshal(d)
		d.mu.RUnlock()
		if err != nil {
			http.Error(w, "openapi document unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})
}

func (d *Document) DocsHandler(specPath string) http.Handler {
	if strings.TrimSpace(specPath) == "" {
		specPath = "/openapi.json"
	}
	const page = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>LlamaCPP Manager API</title>
</head>
<body>
  <div id="app"></div>
  <script id="api-reference" data-url="{{.SpecPath}}"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`
	tmpl := template.Must(template.New("scalar").Parse(page))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.Execute(w, struct{ SpecPath string }{SpecPath: specPath})
	})
}

func JSONResponse(description string, schema Schema) Response {
	return Response{Description: description, Content: map[string]MediaType{"application/json": {Schema: schema}}}
}

func EmptyResponse(description string) Response { return Response{Description: description} }

func ErrorResponse(description string) Response {
	return JSONResponse(description, Schema{Ref: "#/components/schemas/Error"})
}

func ObjectSchema() Schema { return Schema{Type: "object", AdditionalProperties: true} }
func ArraySchema(items Schema) Schema { return Schema{Type: "array", Items: &items} }

func JSONBody(schema Schema, required bool) *RequestBody {
	return &RequestBody{Required: required, Content: map[string]MediaType{"application/json": {Schema: schema}}}
}
