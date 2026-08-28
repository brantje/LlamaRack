package openapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentRegistrationAndHandlers(t *testing.T) {
	doc := New("1.2.3")
	doc.MustRegister(http.MethodGet, "/api/v1/widgets", Operation{
		OperationID: "listWidgets",
		Summary:     "List widgets",
		Responses:   map[string]Response{"200": JSONResponse("Widgets", ArraySchema(ObjectSchema()))},
	})
	if !doc.HasOperation(http.MethodGet, "/api/v1/widgets") {
		t.Fatal("registered operation missing")
	}
	if got := doc.OperationIDs(); len(got) != 1 || got[0] != "listWidgets" {
		t.Fatalf("operation IDs=%v", got)
	}
	if err := doc.Register(http.MethodPost, "/api/v1/widgets", Operation{OperationID: "listWidgets", Summary: "Duplicate", Responses: map[string]Response{"200": EmptyResponse("ok")}}); err == nil {
		t.Fatal("expected duplicate operation ID error")
	}
	if err := doc.Register(http.MethodGet, "/api/v1/widgets", Operation{OperationID: "duplicateRoute", Summary: "Duplicate", Responses: map[string]Response{"200": EmptyResponse("ok")}}); err == nil {
		t.Fatal("expected duplicate route error")
	}

	w := httptest.NewRecorder()
	doc.JSONHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("json status=%d headers=%v", w.Code, w.Header())
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != Version {
		t.Fatalf("openapi=%v", spec["openapi"])
	}
	info, _ := spec["info"].(map[string]any)
	if info["version"] != "1.2.3" {
		t.Fatalf("info=%v", info)
	}

	w = httptest.NewRecorder()
	doc.DocsHandler("/openapi.json").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "@scalar/api-reference") || !strings.Contains(w.Body.String(), "/openapi.json") {
		t.Fatalf("docs status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestDocumentValidationAndMethodHandling(t *testing.T) {
	doc := New("")
	bad := []Operation{
		{Summary: "No id", Responses: map[string]Response{"200": EmptyResponse("ok")}},
		{OperationID: "noSummary", Responses: map[string]Response{"200": EmptyResponse("ok")}},
		{OperationID: "noResponses", Summary: "No responses"},
	}
	for i, operation := range bad {
		if err := doc.Register(http.MethodGet, "/bad", operation); err == nil {
			t.Fatalf("case %d expected validation error", i)
		}
	}
	if err := doc.Register("", "/bad", Operation{OperationID: "badMethod", Summary: "bad", Responses: map[string]Response{"200": EmptyResponse("ok")}}); err == nil {
		t.Fatal("expected method validation error")
	}
	if err := doc.Register(http.MethodGet, "relative", Operation{OperationID: "badPath", Summary: "bad", Responses: map[string]Response{"200": EmptyResponse("ok")}}); err == nil {
		t.Fatal("expected path validation error")
	}

	w := httptest.NewRecorder()
	doc.JSONHandler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/openapi.json", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("json method status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	doc.DocsHandler("").ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/docs", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("docs method status=%d", w.Code)
	}
}
