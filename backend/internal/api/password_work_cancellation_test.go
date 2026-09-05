package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
)

type responseWriterSpy struct {
	http.ResponseWriter
	writeHeaderCalls int
	writeCalls       int
}

func (s *responseWriterSpy) WriteHeader(statusCode int) {
	s.writeHeaderCalls++
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *responseWriterSpy) Write(data []byte) (int, error) {
	s.writeCalls++
	return s.ResponseWriter.Write(data)
}

func TestAdminResetPasswordCanceledRequestWritesNoResponse(t *testing.T) {
	fixture := newAdminFixture(t)
	user, err := fixture.auth.UserByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	principal := managementAuthContext{User: &user, Session: &auth.Session{}}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), managementAuthContextKey{}, principal))
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/1/password", bytes.NewBufferString(`{"password":"replacement-password"}`)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	spy := &responseWriterSpy{ResponseWriter: recorder}
	fixture.handler.ServeHTTP(spy, req)

	if spy.writeHeaderCalls != 0 {
		t.Fatalf("canceled reset called WriteHeader %d times", spy.writeHeaderCalls)
	}
	if spy.writeCalls != 0 {
		t.Fatalf("canceled reset called Write %d times", spy.writeCalls)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("canceled reset wrote response body %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "" {
		t.Fatalf("canceled reset wrote Content-Type %q", got)
	}
	if got := recorder.Header().Get("Retry-After"); got != "" {
		t.Fatalf("canceled reset wrote Retry-After %q", got)
	}
}
