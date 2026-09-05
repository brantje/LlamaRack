package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestVerifyLoginCredentialsReturnsDatabaseErrorsBeforePasswordWork(t *testing.T) {
	service := testService(t)
	if err := service.db.Close(); err != nil {
		t.Fatal(err)
	}

	gate := newPasswordWorkGate(1, 1)
	blocker, err := gate.reserve()
	if err != nil {
		t.Fatal(err)
	}
	queued, err := gate.reserve()
	if err != nil {
		blocker.Release()
		t.Fatal(err)
	}
	defer queued.Release()

	entered := make(chan struct{})
	release := make(chan struct{})
	blockerDone := make(chan error, 1)
	go func() {
		blockerDone <- blocker.run(context.Background(), func() {
			close(entered)
			<-release
		})
	}()
	<-entered

	result := make(chan error, 1)
	go func() {
		_, _, err := service.verifyLoginCredentials(context.Background(), queued, "admin", "wrong-password")
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil {
			close(release)
			<-blockerDone
			blocker.Release()
			t.Fatal("expected database error")
		}
		if errors.Is(err, ErrInvalidCredentials) {
			close(release)
			<-blockerDone
			blocker.Release()
			t.Fatalf("database error was collapsed into invalid credentials: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		<-blockerDone
		blocker.Release()
		t.Fatal("database failure incorrectly waited for Argon2 password work")
	}

	close(release)
	if err := <-blockerDone; err != nil {
		blocker.Release()
		t.Fatal(err)
	}
	blocker.Release()
}
