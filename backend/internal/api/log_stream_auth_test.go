package api

import (
	"net/http"
	"testing"
)

func TestManagementSecurityAuthenticatesLogStreamWithOneTimeTicket(t *testing.T) {
	fixture := newPhase10SecurityFixture(t)
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
	mux.HandleFunc("/api/v1/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		user, authenticatedSession, ok := managementAuthFromRequest(r)
		if !ok || user.ID != login.User.ID || authenticatedSession.ID != session.ID {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "missing management auth context"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := ManagementSecurity(fixture.auth, fixture.network, mux)

	w := phase10Request(t, handler, http.MethodGet, "/api/v1/logs/stream?instance_id=coder&ticket="+ticket, nil, nil, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("ticket stream status=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, handler, http.MethodGet, "/api/v1/logs/stream?instance_id=coder&ticket="+ticket, nil, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replayed ticket status=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, handler, http.MethodGet, "/api/v1/logs/stream?instance_id=coder", nil, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing ticket status=%d body=%s", w.Code, w.Body.String())
	}

	freshTicket, _, err := fixture.auth.IssueWebSocketTicket(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	w = phase10Request(t, handler, http.MethodPost, "/api/v1/logs/stream?instance_id=coder&ticket="+freshTicket, nil, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("non-GET ticket stream status=%d body=%s", w.Code, w.Body.String())
	}
}
