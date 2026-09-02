package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAdminUserAndSessionServiceEdges(t *testing.T) {
	ctx := context.Background()
	s := testService(t)

	s.SetSessionLifetime(0)
	if s.SessionLifetime() != time.Hour {
		t.Fatalf("non-positive lifetime changed service: %v", s.SessionLifetime())
	}
	s.SetSessionLifetime(2 * time.Hour)
	if s.SessionLifetime() != 2*time.Hour {
		t.Fatalf("session lifetime=%v", s.SessionLifetime())
	}

	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "x", "correct-horse-battery"); err == nil {
		t.Fatal("expected short username rejection")
	}
	operator, err := s.CreateUser(ctx, "operator", "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "operator", "operator-password"); err == nil {
		t.Fatal("expected duplicate username rejection")
	}
	if err := s.SetUserEnabled(ctx, 999999, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing enable target error=%v", err)
	}
	if err := s.SetUserEnabled(ctx, operator.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(ctx, operator.ID, false); err != nil {
		t.Fatalf("disabling already disabled user: %v", err)
	}
	if err := s.SetUserEnabled(ctx, operator.ID, true); err != nil {
		t.Fatal(err)
	}

	if err := s.ResetPassword(ctx, operator.ID, "short"); err == nil {
		t.Fatal("expected short reset password rejection")
	}
	if err := s.ResetPassword(ctx, 999999, "replacement-password"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing reset target error=%v", err)
	}
	if err := s.ChangePassword(ctx, operator.ID, "operator-password", "short", ""); err == nil {
		t.Fatal("expected short change password rejection")
	}
	if err := s.ChangePassword(ctx, 999999, "operator-password", "replacement-password", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing change target error=%v", err)
	}

	token, _, _, err := s.LoginWithMetadata(ctx, "operator", "operator-password", "", "edge-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, operator.ID, "operator-password", "replacement-password", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUserWithSession(ctx, token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("password change without keep session must revoke all sessions: %v", err)
	}

	if err := s.DeleteUser(ctx, admin.ID, 999999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing delete target error=%v", err)
	}
	if err := s.DeleteUser(ctx, admin.ID, operator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserByID(ctx, operator.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted user still resolves: %v", err)
	}
}

func TestAPIKeyServiceEdges(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AuthenticateAPIKey(ctx, ""); !errors.Is(err, ErrAPIKeyMissing) {
		t.Fatalf("missing key error=%v", err)
	}
	if err := s.AuthenticateAPIKey(ctx, "definitely-invalid"); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("invalid key error=%v", err)
	}
	if err := s.UpdateAPIKey(ctx, "missing", UpdateAPIKeyInput{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing update error=%v", err)
	}
	if _, _, err := s.RotateAPIKey(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing rotate error=%v", err)
	}

	if _, _, err := s.CreateAPIKeyForUser(ctx, "no-creator", 0); !errors.Is(err, ErrAPIKeyOwnerRequired) {
		t.Fatalf("missing owner error=%v", err)
	}
	key, secret, err := s.CreateAPIKeyForUser(ctx, "owned", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key.CreatedByUserID == nil || *key.CreatedByUserID != admin.ID {
		t.Fatalf("unexpected creator: %+v", key.CreatedByUserID)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAPIKeyEnabled(ctx, key.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err == nil {
		t.Fatal("disabled key should fail")
	}
	rotated, rotatedSecret, err := s.RotateAPIKey(ctx, key.ID)
	if err != nil || rotated.ID != key.ID || rotatedSecret == secret {
		t.Fatalf("rotate disabled key: %+v err=%v", rotated, err)
	}
}
