package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPasswordWorkGateBoundsConcurrencyAndBacklog(t *testing.T) {
	gate := newPasswordWorkGate(2, 4)
	reservations := make([]*passwordWorkReservation, 0, 6)
	for i := 0; i < 6; i++ {
		reservation, err := gate.reserve()
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		reservations = append(reservations, reservation)
	}
	if _, err := gate.reserve(); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("seventh reservation error=%v, want %v", err, ErrPasswordWorkBusy)
	}

	var active int32
	var maximum int32
	entered := make(chan struct{}, len(reservations))
	release := make(chan struct{})
	errs := make(chan error, len(reservations))
	var wg sync.WaitGroup
	for _, reservation := range reservations {
		wg.Add(1)
		go func(reservation *passwordWorkReservation) {
			defer wg.Done()
			defer reservation.Release()
			err := reservation.run(context.Background(), func() {
				current := atomic.AddInt32(&active, 1)
				for {
					observed := atomic.LoadInt32(&maximum)
					if current <= observed || atomic.CompareAndSwapInt32(&maximum, observed, current) {
						break
					}
				}
				entered <- struct{}{}
				<-release
				atomic.AddInt32(&active, -1)
			})
			errs <- err
		}(reservation)
	}

	<-entered
	<-entered
	if got := atomic.LoadInt32(&maximum); got != 2 {
		t.Fatalf("maximum active password work=%d, want 2", got)
	}
	if got := atomic.LoadInt32(&active); got != 2 {
		t.Fatalf("active password work=%d, want 2", got)
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("password work failed: %v", err)
		}
	}
	if len(gate.active) != 0 || len(gate.admitted) != 0 {
		t.Fatalf("gate leaked capacity: active=%d admitted=%d", len(gate.active), len(gate.admitted))
	}
}

func TestPasswordWorkQueuedCancellationReleasesBacklogCapacity(t *testing.T) {
	gate := newPasswordWorkGate(1, 1)
	activeReservation, err := gate.reserve()
	if err != nil {
		t.Fatal(err)
	}
	queuedReservation, err := gate.reserve()
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- activeReservation.run(context.Background(), func() {
			close(entered)
			<-release
		})
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := queuedReservation.run(ctx, func() { called = true }); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation error=%v", err)
	}
	if called {
		t.Fatal("canceled queued password work executed")
	}
	queuedReservation.Release()

	replacement, err := gate.reserve()
	if err != nil {
		t.Fatalf("backlog capacity was not released: %v", err)
	}
	replacement.Release()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	activeReservation.Release()
}

func TestPasswordWorkAdmissionStressRemainsBounded(t *testing.T) {
	gate := newPasswordWorkGate(2, 4)
	const requests = 1000
	attempted := make(chan struct{}, requests)
	hold := make(chan struct{})
	var admitted int32
	var busy int32
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reservation, err := gate.reserve()
			switch {
			case err == nil:
				atomic.AddInt32(&admitted, 1)
				attempted <- struct{}{}
				<-hold
				reservation.Release()
			case errors.Is(err, ErrPasswordWorkBusy):
				atomic.AddInt32(&busy, 1)
				attempted <- struct{}{}
			default:
				t.Errorf("unexpected reservation error: %v", err)
				attempted <- struct{}{}
			}
		}()
	}
	for i := 0; i < requests; i++ {
		<-attempted
	}
	if got := atomic.LoadInt32(&admitted); got != 6 {
		t.Fatalf("simultaneously admitted=%d, want 6", got)
	}
	if got := atomic.LoadInt32(&busy); got != requests-6 {
		t.Fatalf("busy=%d, want %d", got, requests-6)
	}
	close(hold)
	wg.Wait()
	if len(gate.admitted) != 0 {
		t.Fatalf("admission capacity leaked: %d", len(gate.admitted))
	}
}

func TestPasswordWorkSaturationAppliesBeforeLoginLookupAndMutations(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	user, err := service.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	previous := globalPasswordWorkGate
	globalPasswordWorkGate = newPasswordWorkGate(1, 1)
	t.Cleanup(func() { globalPasswordWorkGate = previous })
	first, err := reservePasswordWork()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reservePasswordWork()
	if err != nil {
		first.Release()
		t.Fatal(err)
	}
	defer first.Release()
	defer second.Release()

	if _, err := service.LoginBearerWithMetadata(ctx, "admin", "correct-horse-battery", "", ""); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("known-user login error=%v", err)
	}
	if _, err := service.LoginBearerWithMetadata(ctx, "missing-user", "correct-horse-battery", "", ""); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("unknown-user login error=%v", err)
	}
	if _, _, _, err := service.LoginWithMetadata(ctx, "missing-user", "correct-horse-battery", "", ""); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("legacy login error=%v", err)
	}
	if _, err := service.CreateUser(ctx, "operator", "correct-horse-battery"); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("create user error=%v", err)
	}
	if err := service.ResetPassword(ctx, user.ID, "replacement-password"); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("reset password error=%v", err)
	}
	if err := service.ChangePassword(ctx, user.ID, "correct-horse-battery", "replacement-password", ""); !errors.Is(err, ErrPasswordWorkBusy) {
		t.Fatalf("change password error=%v", err)
	}
}

func TestArgon2ProductionCallsStayBehindPasswordWorkGate(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "argon2.IDKey") && entry.Name() != "passwords.go" {
			t.Fatalf("direct Argon2 call bypasses password work gate in %s", entry.Name())
		}
	}
}
