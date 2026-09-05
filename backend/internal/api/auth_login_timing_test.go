package api

import (
	"net/http"
	"testing"

	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestAdminInvalidLoginResponsesDoNotRevealAccountState(t *testing.T) {
	f := newAuthSecurityFixture(t)
	if _, err := f.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.auth.CreateUser(t.Context(), "disabled", "disabled-user-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.SetUserEnabled(t.Context(), disabled.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.settings.Set(t.Context(), settings.LoginFailureThreshold, 99); err != nil {
		t.Fatal(err)
	}

	attempts := []struct {
		name     string
		username string
		password string
	}{
		{name: "unknown", username: "missing-user", password: "definitely-wrong-password"},
		{name: "disabled", username: "disabled", password: "definitely-wrong-password"},
		{name: "wrong-password", username: "admin", password: "definitely-wrong-password"},
	}

	var wantBody string
	for index, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			w := adminRequest(t, f.handler, http.MethodPost, "/api/v1/auth/login", map[string]string{
				"username": attempt.username,
				"password": attempt.password,
			}, nil, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if index == 0 {
				wantBody = w.Body.String()
				return
			}
			if got := w.Body.String(); got != wantBody {
				t.Fatalf("invalid-login body differs by account state: got=%q want=%q", got, wantBody)
			}
		})
	}
}
