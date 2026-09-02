package api

import (
	"net/http"
	"testing"
)

func TestManagementSecurityAuthenticatesBrowserStreamsWithOneTimeTicket(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		withID  string
		missing string
	}{
		{
			name:    "log stream",
			path:    "/api/v1/logs/stream",
			withID:  "/api/v1/logs/stream?instance_id=coder&ticket=",
			missing: "/api/v1/logs/stream?instance_id=coder",
		},
		{
			name:    "download websocket",
			path:    "/api/v1/downloads/ws",
			withID:  "/api/v1/downloads/ws?ticket=",
			missing: "/api/v1/downloads/ws",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAuthSecurityFixture(t)
			if _, err := fixture.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery"); err != nil {
				t.Fatal(err)
			}
			login, err := fixture.auth.LoginBearerWithMetadata(t.Context(), "admin", "correct-horse-battery", "192.0.2.10", "log-stream-test")
			if err != nil {
				t.Fatal(err)
			}
			_, session, err := fixture.auth.AuthenticateBearer(t.Context(), login.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			ticket, _, err := fixture.auth.IssueWebSocketTicket(t.Context(), session)
			if err != nil {
				t.Fatal(err)
			}

			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, func(w http.ResponseWriter, r *http.Request) {
				principal, ok := managementAuthFromRequest(r)
				if !ok || principal.User == nil || principal.Session == nil || principal.User.ID != login.User.ID || principal.Session.ID != session.ID {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "missing management auth context"})
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			handler := ManagementSecurity(fixture.auth, fixture.network, mux)

			w := adminRequest(t, handler, http.MethodGet, tc.withID+ticket, nil, nil, nil)
			if w.Code != http.StatusNoContent {
				t.Fatalf("ticket stream status=%d body=%s", w.Code, w.Body.String())
			}

			w = adminRequest(t, handler, http.MethodGet, tc.withID+ticket, nil, nil, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("replayed ticket status=%d body=%s", w.Code, w.Body.String())
			}

			w = adminRequest(t, handler, http.MethodGet, tc.missing, nil, nil, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("missing ticket status=%d body=%s", w.Code, w.Body.String())
			}

			freshTicket, _, err := fixture.auth.IssueWebSocketTicket(t.Context(), session)
			if err != nil {
				t.Fatal(err)
			}
			w = adminRequest(t, handler, http.MethodPost, tc.withID+freshTicket, nil, nil, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("non-GET ticket stream status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
