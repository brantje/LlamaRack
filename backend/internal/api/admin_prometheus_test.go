package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestAdminPrometheusAuthTokenMaskedReplaceClearAndOmit(t *testing.T) {
	f := newAdminFixture(t)
	const token = "dashboard-metrics-secret"

	w := doRequest(t, f.handler, http.MethodGet, "/api/v1/settings/general", nil, f.cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", w.Code, w.Body.String())
	}
	assertPrometheusTokenJSON(t, w.Body.Bytes(), false, "default", "")

	w = doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"prometheus_auth_token": token}, f.cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("put=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatalf("PUT echoed plaintext token: %s", w.Body.String())
	}
	assertPrometheusTokenJSON(t, w.Body.Bytes(), true, "database", "dashboar")
	got, err := f.secrets.GetSecret(t.Context(), huggingface.SecretPrometheusAuthToken)
	if err != nil || got != token {
		t.Fatalf("stored secret=%q err=%v", got, err)
	}
	assertManagerSettingsHasNoPrometheusToken(t, f.db, token)

	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/settings/general", nil, f.cookie)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), token) {
		t.Fatalf("GET leaked token: %d %s", w.Code, w.Body.String())
	}
	assertPrometheusTokenJSON(t, w.Body.Bytes(), true, "database", "dashboar")

	w = doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"idle_unload_seconds": 600}, f.cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("omit put=%d body=%s", w.Code, w.Body.String())
	}
	assertPrometheusTokenJSON(t, w.Body.Bytes(), true, "database", "dashboar")
	got, err = f.secrets.GetSecret(t.Context(), huggingface.SecretPrometheusAuthToken)
	if err != nil || got != token {
		t.Fatalf("omitted secret=%q err=%v", got, err)
	}

	w = doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"prometheus_auth_token": ""}, f.cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("clear put=%d body=%s", w.Code, w.Body.String())
	}
	assertPrometheusTokenJSON(t, w.Body.Bytes(), false, "default", "")
	got, err = f.secrets.GetSecret(t.Context(), huggingface.SecretPrometheusAuthToken)
	if err != nil || got != "" {
		t.Fatalf("cleared secret=%q err=%v", got, err)
	}
}

func TestAdminPrometheusAuthTokenRejectsInvalidCompanionSettingWithoutMutatingSecret(t *testing.T) {
	f := newAdminFixture(t)
	const token = "dashboard-metrics-secret"
	w := doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{
		"prometheus_auth_token":    token,
		"session_lifetime_seconds": 1,
	}, f.cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mixed invalid put=%d body=%s", w.Code, w.Body.String())
	}
	got, err := f.secrets.GetSecret(t.Context(), huggingface.SecretPrometheusAuthToken)
	if err != nil || got != "" {
		t.Fatalf("token mutated after rejected companion setting: secret=%q err=%v", got, err)
	}
	assertManagerSettingsHasNoPrometheusToken(t, f.db, token)
}

func TestAdminPrometheusAuthTokenEnvFallbackMasked(t *testing.T) {
	t.Setenv("LLAMARACK_PROMETHEUS_AUTH_TOKEN", "environment-metrics-token")
	f := newAdminFixture(t)
	w := doRequest(t, f.handler, http.MethodGet, "/api/v1/settings/general", nil, f.cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "environment-metrics-token") {
		t.Fatalf("GET echoed env token: %s", w.Body.String())
	}
	assertPrometheusTokenJSON(t, w.Body.Bytes(), true, "environment", "environm")
}

func TestAdminPrometheusAuthTokenRequiresSecretsStore(t *testing.T) {
	f := newAdminFixture(t)
	handler := NewAdminHandler(f.auth, f.settings, nil, managersecurity.NewNetwork(f.settings), func() (llamacpp.Profile, error) {
		return llamacpp.Profile{}, nil
	})
	w := doRequest(t, handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"prometheus_auth_token": "secret"}, f.cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("nil store put=%d body=%s", w.Code, w.Body.String())
	}
}

func assertPrometheusTokenJSON(t *testing.T, body []byte, configured bool, source, prefix string) {
	t.Helper()
	var general settings.General
	if err := json.Unmarshal(body, &general); err != nil {
		t.Fatal(err)
	}
	if general.PrometheusToken.Configured != configured || general.PrometheusToken.Source != source || general.PrometheusToken.Prefix != prefix || !general.PrometheusToken.Editable {
		t.Fatalf("prometheus token=%+v want configured=%v source=%s prefix=%q", general.PrometheusToken, configured, source, prefix)
	}
}

func assertManagerSettingsHasNoPrometheusToken(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM manager_settings WHERE setting_key=?`, settings.PrometheusAuthToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("prometheus still stored in manager_settings: count=%d", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM manager_settings WHERE instr(setting_value, ?) > 0`, token).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("raw token present in manager_settings: count=%d", count)
	}
}
