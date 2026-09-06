package slots

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseSlotID(t *testing.T) {
	valid := []struct {
		in   string
		want uint64
	}{
		{in: "0", want: 0},
		{in: "7", want: 7},
		{in: "001", want: 1},
	}
	for _, tc := range valid {
		got, err := ParseSlotID(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseSlotID(%q)=%d err=%v want=%d", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "-1", "+7", "1e2", "1.0", "../1", "1/2", "%2e%2e%2fprobe", "1%2F2", " 7", "7 ", "abc"} {
		if _, err := ParseSlotID(in); err == nil {
			t.Fatalf("ParseSlotID(%q) accepted", in)
		}
	}
}

func TestUpstreamPath(t *testing.T) {
	got, err := UpstreamPath(http.MethodGet, "")
	if err != nil || got != "/slots" {
		t.Fatalf("GET path=%q err=%v", got, err)
	}
	for _, tc := range []struct {
		id   string
		want string
	}{
		{id: "0", want: "/slots/0"},
		{id: "3", want: "/slots/3"},
		{id: "7", want: "/slots/7"},
		{id: "001", want: "/slots/1"},
	} {
		got, err = UpstreamPath(http.MethodPost, tc.id)
		if err != nil || got != tc.want {
			t.Fatalf("POST id=%q path=%q err=%v want=%q", tc.id, got, err, tc.want)
		}
	}
}

func TestUpstreamPathRejectsMalformedIDs(t *testing.T) {
	for _, id := range []string{"", "-1", "abc", "../1", "1/2", "%2e%2e%2fprobe", "1%2F2", ".."} {
		got, err := UpstreamPath(http.MethodPost, id)
		if err == nil {
			t.Fatalf("POST id=%q path=%q", id, got)
		}
		if got != "" {
			t.Fatalf("POST id=%q leaked path=%q", id, got)
		}
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
	upstreamPath, err := UpstreamPath(http.MethodGet, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Proxy(rec, req, worker.URL, upstreamPath, nil, true); err != nil {
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
