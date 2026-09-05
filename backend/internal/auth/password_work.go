package auth

import (
	"context"
	"sync"
)

const (
	passwordWorkMaxActive  = 2
	passwordWorkMaxBacklog = 4
)

var globalPasswordWorkGate = newPasswordWorkGate(passwordWorkMaxActive, passwordWorkMaxBacklog)

type passwordWorkGate struct {
	active   chan struct{}
	admitted chan struct{}
}

type passwordWorkReservation struct {
	gate *passwordWorkGate
	once sync.Once
}

func newPasswordWorkGate(maxActive, maxBacklog int) *passwordWorkGate {
	if maxActive < 1 {
		panic("password work max active must be positive")
	}
	if maxBacklog < 0 {
		panic("password work max backlog must not be negative")
	}
	return &passwordWorkGate{
		active:   make(chan struct{}, maxActive),
		admitted: make(chan struct{}, maxActive+maxBacklog),
	}
}

func reservePasswordWork() (*passwordWorkReservation, error) {
	return globalPasswordWorkGate.reserve()
}

func (g *passwordWorkGate) reserve() (*passwordWorkReservation, error) {
	select {
	case g.admitted <- struct{}{}:
		return &passwordWorkReservation{gate: g}, nil
	default:
		return nil, ErrPasswordWorkBusy
	}
}

func (r *passwordWorkReservation) run(ctx context.Context, work func()) error {
	select {
	case r.gate.active <- struct{}{}:
		defer func() { <-r.gate.active }()
		work()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *passwordWorkReservation) Release() {
	if r == nil || r.gate == nil {
		return
	}
	r.once.Do(func() { <-r.gate.admitted })
}
