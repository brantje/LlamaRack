package slots

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUpstreamPath(t *testing.T) {
	if got := UpstreamPath(http.MethodGet, ""); got != "/slots" {
		t.Fatalf("GET path=%q", got)
	}
	if got := UpstreamPath(http.MethodPost, "3"); got != "/slots/3" {
		t.Fatalf("POST path=%q", got)
	}
}

func TestSanitizeQueryDropsModel(t *testing.T) {
	raw := url.Values{
		"model":  {"gateway-model"},
		"action": {"save"},
		"extra":  {"1"},
	}
	out := SanitizeQuery(raw)
	if out.Get("model") != "" || out.Get("action") != "save" || out.Get("extra") != "1" {
		t.Fatalf("sanitized=%v", out)
	}
}

func TestValidateAction(t *testing.T) {
	for _, action := range []string{"save", "restore", "erase"} {
		if err := ValidateAction(action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if err := ValidateAction(""); err == nil {
		t.Fatal("expected missing action error")
	}
	if err := ValidateAction("drop"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unexpected=%v", err)
	}
}

func TestValidateFilename(t *testing.T) {
	for _, name := range []string{"", "../x", `a\b`, "dir/file"} {
		if err := ValidateFilename(name); err == nil {
			t.Fatalf("expected reject for %q", name)
		}
	}
	if err := ValidateFilename("slot-1.json"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequestBody(t *testing.T) {
	if _, err := ValidateRequestBody(nil, "erase"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRequestBody([]byte(`{"filename":"../bad"}`), "save"); err == nil {
		t.Fatal("expected filename rejection")
	}
	if _, err := ValidateRequestBody([]byte(`{"filename":"ok.json"}`), "restore"); err != nil {
		t.Fatal(err)
	}
}

func TestProxyRewritesPathQueryAndMapsNotImplemented(t *testing.T) {
	var gotPath, gotQuery string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "auth leaked", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(worker.Close)

	req := httptest.NewRequest(http.MethodGet, "/v1/slots?model=gateway-model&action=save", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	if err := Proxy(rec, req, worker.URL, UpstreamPath(http.MethodGet, ""), nil, true); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/slots" || strings.Contains(gotQuery, "model=") || !strings.Contains(gotQuery, "action=save") {
		t.Fatalf("upstream path=%q query=%q", gotPath, gotQuery)
	}
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	errObj, _ := payload["error"].(map[string]any)
	if errObj["code"] != "not_implemented" {
		t.Fatalf("payload=%v", payload)
	}
}
