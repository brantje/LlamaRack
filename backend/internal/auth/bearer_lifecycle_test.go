package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentSigningKeyAndBearerLoginLifecycle(t *testing.T) {
	ctx := t.Context()
	s := testService(t)
	keyDir := t.TempDir()

	if err := s.UsePersistentSigningKey(keyDir); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(keyDir, signingKeyFilename)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("signing key permissions=%o want=600", info.Mode().Perm())
	}
	firstPublic := append(ed25519.PublicKey(nil), s.jwtPublic...)

	reloaded := New(s.db, s.SessionLifetime())
	if err := reloaded.UsePersistentSigningKey(keyDir); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstPublic, reloaded.jwtPublic) {
		t.Fatal("persistent signing key changed after reload")
	}

	admin, err := reloaded.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.LoginBearerWithMetadata(ctx, "admin", "wrong-password", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong bearer password err=%v", err)
	}

	result, err := reloaded.LoginBearerWithMetadata(ctx, " admin ", "correct-horse-battery", "192.0.2.44", "bearer-lifecycle-test")
	if err != nil {
		t.Fatal(err)
	}
	if result.AccessToken == "" || result.TokenType != "Bearer" || result.User.ID != admin.ID || result.User.LastLoginAt == nil {
		t.Fatalf("unexpected bearer login result: %+v", result)
	}
	user, session, err := reloaded.AuthenticateBearer(ctx, result.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != admin.ID || session.ID == "" || session.JTI == "" || session.RemoteAddress != "192.0.2.44" || session.UserAgent != "bearer-lifecycle-test" {
		t.Fatalf("unexpected authenticated bearer user/session: user=%+v session=%+v", user, session)
	}
	if _, _, err := reloaded.AuthenticateBearer(ctx, "not-a-jwt"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalid bearer token err=%v", err)
	}
	if err := reloaded.RevokeSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reloaded.AuthenticateBearer(ctx, result.AccessToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked bearer session err=%v", err)
	}

	operator, err := reloaded.CreateUser(ctx, "operator", "another-correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if operator.ID == admin.ID {
		t.Fatal("operator unexpectedly reused admin id")
	}
	if err := reloaded.SetUserEnabled(ctx, admin.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.LoginBearerWithMetadata(ctx, "admin", "correct-horse-battery", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user bearer login err=%v", err)
	}
}

func TestLoadOrCreateEd25519KeyFormatsAndInvalidData(t *testing.T) {
	root := t.TempDir()

	createdPath := filepath.Join(root, "nested", "created.key")
	created, err := loadOrCreateEd25519Key(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != ed25519.PrivateKeySize {
		t.Fatalf("created private key length=%d", len(created))
	}
	createdAgain, err := loadOrCreateEd25519Key(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(created, createdAgain) {
		t.Fatal("seed-backed key did not reload identically")
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "private.key")
	if err := os.WriteFile(privatePath, privateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	loadedPrivate, err := loadOrCreateEd25519Key(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(privateKey, loadedPrivate) {
		t.Fatal("full private key did not reload identically")
	}

	invalidPath := filepath.Join(root, "invalid.key")
	if err := os.WriteFile(invalidPath, []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateEd25519Key(invalidPath); err == nil {
		t.Fatal("invalid signing key length should fail")
	}
}
