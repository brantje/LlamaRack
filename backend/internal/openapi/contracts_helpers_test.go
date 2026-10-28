package openapi

import (
	"net/http"
	"testing"
)

// Generated API contracts must preserve content types, requiredness and
// references so that clients can consume multipart and error responses.
func TestOpenAPIRequestAndResponseHelperContracts(t *testing.T) {
	const errorRef = "#/components/schemas/Error"
	errResp := ErrorResponse("Not found")
	media, ok := errResp.Content["application/json"]
	if !ok || errResp.Description != "Not found" || media.Schema.Ref != errorRef {
		t.Fatalf("unexpected API error schema: %+v", errResp)
	}
	shape := Schema{Type: "object", Properties: map[string]Schema{"id": {Type: "string"}}}
	for _, required := range []bool{false, true} {
		jsonBody := JSONBody(shape, required)
		if jsonBody.Required != required || jsonBody.Content["application/json"].Schema.Type != "object" ||
			jsonBody.Content["application/json"].Schema.Properties["id"].Type != "string" {
			t.Fatalf("JSON request schema=%+v", jsonBody)
		}
		upload := MultipartBody(required)
		if upload.Required != required || upload.Content["multipart/form-data"].Schema.Type != "object" {
			t.Fatalf("multipart request schema=%+v", upload)
		}
	}

	doc := New("1.2.3")
	doc.MustRegister(http.MethodGet, "/items", Operation{
		OperationID: "getItems", Summary: "Get items",
		Responses: map[string]Response{"404": errResp},
	})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate operation registration should panic")
		}
	}()
	doc.MustRegister(http.MethodPost, "/items", Operation{
		OperationID: "getItems", Summary: "Duplicate operation",
		Responses: map[string]Response{"200": EmptyResponse("ok")},
	})
}
