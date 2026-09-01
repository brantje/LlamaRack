package supervisor

import "sync"

type runtimeEventBus struct {
	mu   sync.Mutex
	subs map[chan Runtime]struct{}
}

var runtimeEventBuses sync.Map

func runtimeBusFor(s *Supervisor) *runtimeEventBus {
	if existing, ok := runtimeEventBuses.Load(s); ok {
		return existing.(*runtimeEventBus)
	}
	bus := &runtimeEventBus{subs: map[chan Runtime]struct{}{}}
	existing, _ := runtimeEventBuses.LoadOrStore(s, bus)
	return existing.(*runtimeEventBus)
}

// SubscribeRuntimes returns an atomic snapshot of workers known to the
// supervisor plus a live stream of observed runtime transitions after that
// snapshot. Publishing is non-blocking so a slow management client can never
// stall process supervision.
func (s *Supervisor) SubscribeRuntimes() ([]Runtime, <-chan Runtime, func()) {
	bus := runtimeBusFor(s)

	// Runtime transitions are published while s.mu is held. Holding the same
	// lock while registering the subscriber makes the snapshot + subscription
	// boundary atomic: no transition can fall between them.
	s.mu.Lock()
	bus.mu.Lock()
	snapshot := make([]Runtime, 0, len(s.workers))
	for _, worker := range s.workers {
		snapshot = append(snapshot, worker.runtime)
	}
	ch := make(chan Runtime, 64)
	bus.subs[ch] = struct{}{}
	bus.mu.Unlock()
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			bus.mu.Lock()
			if _, ok := bus.subs[ch]; ok {
				delete(bus.subs, ch)
				close(ch)
			}
			bus.mu.Unlock()
		})
	}
	return snapshot, ch, cancel
}

// PublishRuntime emits an observed runtime snapshot to subscribers without
// mutating supervisor worker state. Lifecycle overlays such as startup backoff
// use this so management clients see RetryAfter as soon as it is recorded.
func (s *Supervisor) PublishRuntime(runtime Runtime) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emitRuntimeLocked(runtime)
}

// emitRuntimeLocked must be called while s.mu is held so event ordering follows
// the exact ordering of supervisor state mutations.
func (s *Supervisor) emitRuntimeLocked(runtime Runtime) {
	bus := runtimeBusFor(s)
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for ch := range bus.subs {
		select {
		case ch <- runtime:
		default:
			// Keep the newest observed state if a subscriber falls behind.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- runtime:
			default:
			}
		}
	}
}
