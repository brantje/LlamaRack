package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/observability"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

const (
	testTraceHeader  = "aee4ef30-0d78-40a5-b71c-ef0d9d04f47f"
	testTraceSession = "11111111-2222-4333-8444-555555555555"
	testTraceBody    = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

func gatewayRequestWithHeaders(t *testing.T, g http.Handler, method, path, secret, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestTraceResolutionPrecedenceAndGeneration(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if got := resolveTraceID(r, ""); len(got) != 36 || got[14] != '4' {
		t.Fatalf("generated trace=%q", got)
	}
	r.Header.Set(headerSessionID, testTraceSession)
	if got := resolveTraceID(r, testTraceBody); got != testTraceBody {
		t.Fatalf("session must not replace body trace, got=%q", got)
	}
	r.Header.Set(headerTraceID, strings.ToUpper(testTraceHeader))
	if got := resolveTraceID(r, testTraceBody); got != testTraceHeader {
		t.Fatalf("header trace=%q", got)
	}
	r.Header.Set(headerTraceID, "not-a-uuid")
	if got := resolveTraceID(r, testTraceBody); got != testTraceBody {
		t.Fatalf("invalid trace header should fall through to body trace, got=%q", got)
	}
	r.Header.Del(headerTraceID)
	generated := resolveTraceID(r, "")
	if generated == testTraceSession {
		t.Fatalf("session id reused as trace=%q", generated)
	}
	if _, ok := normalizeUUID(generated); !ok {
		t.Fatalf("generated trace invalid=%q", generated)
	}
	if _, ok := normalizeUUID("bad"); ok {
		t.Fatal("invalid UUID accepted")
	}
}

func TestCallTypeMapping(t *testing.T) {
	cases := map[string]string{
		"/v1/chat/completions":              "chat_completion",
		"/v1/completions":                   "completion",
		"/v1/responses":                     "response",
		"/v1/embeddings":                    "embedding",
		"/v1/unknown":                       "",
		"/v1/responses/input_tokens":        "response_input_tokens",
		"/v1/chat/completions/input_tokens": "chat_input_tokens",
		"/v1/rerank":                        "rerank",
		"/v1/audio/transcriptions":          "transcription",
		"/v1/slots":                         "slots_list",
	}
	for path, want := range cases {
		if got := callType(path); got != want {
			t.Fatalf("%s call type=%q want=%q", path, got, want)
		}
	}
}

func TestPersistedClientIPUsesTrustedProxyResolver(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	store := settings.New(f.db, settings.Defaults{})
	if _, err := store.Set(ctx, settings.TrustedProxies, "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	f.gateway.SetNetwork(managersecurity.NewNetwork(store))

	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "untrusted direct client cannot spoof forwarding headers",
			remoteAddr: "203.0.113.10:4444",
			headers: map[string]string{
				"Forwarded":       "for=198.51.100.1",
				"X-Forwarded-For": "198.51.100.2",
				"X-Real-IP":       "198.51.100.3",
			},
			want: "203.0.113.10",
		},
		{
			name:       "trusted proxy uses forwarded",
			remoteAddr: "10.0.0.2:1234",
			headers:    map[string]string{"Forwarded": `for="[2001:0db8::1]:444";proto=https, for=10.0.0.3`},
			want:       "2001:db8::1",
		},
		{
			name:       "trusted proxy uses x forwarded for",
			remoteAddr: "10.0.0.2:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9:555, 10.0.0.3"},
			want:       "203.0.113.9",
		},
		{
			name:       "trusted proxy uses x real ip fallback",
			remoteAddr: "10.0.0.2:1234",
			headers:    map[string]string{"X-Real-IP": "198.51.100.10:777"},
			want:       "198.51.100.10",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gateway-model"}`))
			r.RemoteAddr = tc.remoteAddr
			r.Header.Set("Authorization", "Bearer wrong")
			for key, value := range tc.headers {
				r.Header.Set(key, value)
			}
			w := httptest.NewRecorder()
			f.gateway.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			record, err := f.observability.GetRequestByRequestID(ctx, w.Header().Get(headerRequestID))
			if err != nil {
				t.Fatal(err)
			}
			if record.ClientIP != tc.want {
				t.Fatalf("persisted client ip=%q want=%q", record.ClientIP, tc.want)
			}
		})
	}
}

func TestEarlyGatewayFailuresArePersistedWithRequestAndTraceIDs(t *testing.T) {
	f := newGatewayFixture(t, false)
	cases := []struct {
		name, secret, path, body string
		want                     int
		wantInstance             string
	}{
		{"invalid-key", "wrong", "/v1/chat/completions", `{"model":"gateway-model"}`, http.StatusUnauthorized, ""},
		{"malformed", f.secret, "/v1/chat/completions", `{`, http.StatusBadRequest, ""},
		{"missing-model", f.secret, "/v1/chat/completions", `{}`, http.StatusBadRequest, ""},
		{"unsupported", f.secret, "/v1/does-not-exist", `{"model":"gateway-model"}`, http.StatusNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := gatewayRequestWithHeaders(t, f.gateway, http.MethodPost, tc.path, tc.secret, tc.body, map[string]string{
				headerTraceID: testTraceHeader,
				"User-Agent":  "request-log-test/1.0",
			})
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			requestID := w.Header().Get(headerRequestID)
			if requestID == "" || w.Header().Get(headerTraceID) != testTraceHeader {
				t.Fatalf("headers=%v", w.Header())
			}
			record, err := f.observability.GetRequestByRequestID(context.Background(), requestID)
			if err != nil {
				t.Fatal(err)
			}
			if record.StatusCode != tc.want || record.Result != "error" || record.TraceID != testTraceHeader || record.InstanceID != tc.wantInstance {
				t.Fatalf("record=%+v", record)
			}
			if record.ClientIP != "192.0.2.1" || record.UserAgent != "request-log-test/1.0" {
				t.Fatalf("client metadata=%+v", record)
			}
			if tc.name == "invalid-key" && record.APIKey != nil {
				t.Fatalf("invalid key should not persist key identity: %+v", record.APIKey)
			}
		})
	}
}

func TestLiteLLMBodyTraceRemainsSeparateFromSessionIdentity(t *testing.T) {
	f := newGatewayFixture(t, false)
	body := `{"model":"gateway-model","litellm_metadata":{"trace_id":"` + testTraceBody + `"}}`
	w := gatewayRequestWithHeaders(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, body, nil)
	if w.Header().Get(headerTraceID) != testTraceBody {
		t.Fatalf("body trace header=%q", w.Header().Get(headerTraceID))
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil || record.TraceID != testTraceBody {
		t.Fatalf("body trace record=%+v err=%v", record, err)
	}

	w = gatewayRequestWithHeaders(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, body, map[string]string{headerSessionID: testTraceSession})
	if w.Header().Get(headerTraceID) != testTraceBody {
		t.Fatalf("session must not replace body trace header=%q", w.Header().Get(headerTraceID))
	}
}

func TestMultipleRequestsShareTraceAndSuccessReturnsTrace(t *testing.T) {
	f := newGatewayFixture(t, true)
	for i := 0; i < 2; i++ {
		w := gatewayRequestWithHeaders(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"gateway-model"}`, map[string]string{headerTraceID: testTraceHeader})
		if w.Code != http.StatusOK || w.Header().Get(headerTraceID) != testTraceHeader || w.Header().Get(headerRequestID) == "" {
			t.Fatalf("response=%d headers=%v", w.Code, w.Header())
		}
	}
	items, err := f.observability.ListRequests(context.Background(), observability.RequestFilters{TraceID: testTraceHeader, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].StartedAt > items[1].StartedAt {
		t.Fatalf("trace order=%+v", items)
	}
	for _, item := range items {
		if item.TraceID != testTraceHeader || item.CallType != "chat_completion" {
			t.Fatalf("trace item=%+v", item)
		}
	}
}
