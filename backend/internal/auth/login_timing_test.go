package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvalidLoginClassesUseBoundedPasswordWork(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	if _, err := service.Bootstrap(ctx, "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	disabled, err := service.CreateUser(ctx, "disabled", "disabled-user-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetUserEnabled(ctx, disabled.ID, false); err != nil {
		t.Fatal(err)
	}

	loginPaths := []struct {
		name string
		login func(context.Context, string, string) error
	}{
		{
			name: "bearer",
			login: func(ctx context.Context, username, password string) error {
				_, err := service.LoginBearerWithMetadata(ctx, username, password, "192.0.2.10", "timing-test")
				return err
			},
		},
		{
			name: "legacy",
			login: func(ctx context.Context, username, password string) error {
				_, _, _, err := service.LoginWithMetadata(ctx, username, password, "192.0.2.10", "timing-test")
				return err
			},
		},
	}

	for _, path := range loginPaths {
		for _, username := range []string{"missing-user", "disabled"} {
			t.Run(path.name+"/"+username, func(t *testing.T) {
				assertLoginWaitsForPasswordWork(t, path.login, username)
			})
		}
	}
}

func TestDummyPasswordHashUsesCurrentParameters(t *testing.T) {
	memory, iterations, threads, _, expected, ok := parsePasswordHash(dummyPasswordHash)
	if !ok {
		t.Fatal("dummy password hash must parse")
	}
	if memory != argonMemory || iterations != argonTime || threads != argonThreads || len(expected) != argonKeyLength {
		t.Fatalf("dummy hash parameters m=%d t=%d p=%d len=%d do not match current password parameters", memory, iterations, threads, len(expected))
	}
	if passwordNeedsRehash(dummyPasswordHash) {
		t.Fatal("dummy password hash must use current password parameters")
	}
}

func assertLoginWaitsForPasswordWork(t *testing.T, login func(context.Context, string, string) error, username string) {
	t.Helper()
	previous := globalPasswordWorkGate
	gate := newPasswordWorkGate(1, 1)
	globalPasswordWorkGate = gate
	defer func() { globalPasswordWorkGate = previous }()

	blocker, err := gate.reserve()
	if err != nil {
		t.Fatal(err)
	}
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

	ctx, cancel := context.WithCancel(context.Background())
	loginDone := make(chan error, 1)
	go func() {
		loginDone <- login(ctx, username, "definitely-wrong-password")
	}()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for len(gate.admitted) < 2 {
		select {
		case err := <-loginDone:
			cancel()
			close(release)
			<-blockerDone
			blocker.Release()
			t.Fatalf("login returned before entering password work: %v", err)
		case <-ticker.C:
		case <-timeout.C:
			cancel()
			close(release)
			<-blockerDone
			blocker.Release()
			t.Fatal("login did not reserve queued password work")
		}
	}

	cancel()
	if err := <-loginDone; !errors.Is(err, context.Canceled) {
		close(release)
		<-blockerDone
		blocker.Release()
		t.Fatalf("queued login error=%v, want %v", err, context.Canceled)
	}
	close(release)
	if err := <-blockerDone; err != nil {
		blocker.Release()
		t.Fatal(err)
	}
	blocker.Release()
	if len(gate.active) != 0 || len(gate.admitted) != 0 {
		t.Fatalf("password work gate leaked capacity: active=%d admitted=%d", len(gate.active), len(gate.admitted))
	}
}
