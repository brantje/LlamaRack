package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const (
	LeasePending   = "pending"
	LeaseCommitted = "committed"

	defaultLeaseTTL = 180 * time.Second
)

type GPUReservation struct {
	DeviceID string
	Bytes    int64
}

type ResourceLease struct {
	ID         string
	InstanceID string
	Placement  Placement
	GPUs       []GPUReservation
	HostRAM    int64
	State      string
	ExpiresAt  time.Time
}

// Credit treats another Instance's reserved/estimated capacity as available to
// the acquiring start (resource-pressure eviction). A credited Instance cannot
// be credited to two in-flight starts at once.
type Credit struct {
	InstanceID string
	Bytes      int64
}

type AcquireRequest struct {
	InstanceID string
	Snapshot   hardware.Snapshot
	Placement  PlacementRequest
	Credits    []Credit
	HostRAM    int64
}

type Ledger struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	newID   func() string
	leases  map[string]*ResourceLease
	byInst  map[string]string
	claimed map[string]string
}

func NewLedger() *Ledger {
	return NewLedgerWithTTL(defaultLeaseTTL)
}

func NewLedgerWithTTL(ttl time.Duration) *Ledger {
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	return &Ledger{
		ttl:     ttl,
		now:     time.Now,
		newID:   newLeaseID,
		leases:  map[string]*ResourceLease{},
		byInst:  map[string]string{},
		claimed: map[string]string{},
	}
}

func (l *Ledger) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	l.mu.Lock()
	l.now = now
	l.mu.Unlock()
}

// Acquire atomically places against the snapshot minus existing leases and,
// when the placement fits, records a pending lease owned by InstanceID.
func (l *Ledger) Acquire(req AcquireRequest) (ResourceLease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()

	instanceID := req.InstanceID
	if instanceID == "" {
		return ResourceLease{}, errors.New("lease instance id is required")
	}
	l.releaseInstanceLocked(instanceID)

	usableCredits, creditBytes := l.usableCreditsLocked(instanceID, req.Credits)
	adjusted := adjustSnapshot(req.Snapshot, l.occupancyLocked(instanceID, usableCredits), creditBytes)
	placement, err := PlanPlacement(adjusted, req.Placement)
	if err != nil {
		return ResourceLease{}, err
	}
	if !placement.Fits {
		return ResourceLease{Placement: placement}, nil
	}

	lease := &ResourceLease{
		ID:         l.newID(),
		InstanceID: instanceID,
		Placement:  placement,
		GPUs:       reservationsFor(placement, adjusted, req.Placement),
		HostRAM:    req.HostRAM,
		State:      LeasePending,
		ExpiresAt:  l.now().Add(l.ttl),
	}
	l.leases[lease.ID] = lease
	l.byInst[instanceID] = lease.ID
	for victim := range usableCredits {
		l.claimed[victim] = lease.ID
	}
	return cloneLease(lease), nil
}

func (l *Ledger) Commit(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	lease := l.leases[id]
	if lease == nil {
		return errors.New("unknown resource lease")
	}
	if lease.State != LeasePending {
		return errors.New("resource lease is not pending")
	}
	lease.State = LeaseCommitted
	lease.ExpiresAt = time.Time{}
	return nil
}

func (l *Ledger) CommitInstance(instanceID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.byInst[instanceID]
	if id == "" {
		return nil
	}
	lease := l.leases[id]
	if lease == nil {
		return nil
	}
	if lease.State != LeasePending {
		return errors.New("resource lease is not pending")
	}
	lease.State = LeaseCommitted
	lease.ExpiresAt = time.Time{}
	return nil
}

func (l *Ledger) Release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releaseLocked(id)
}

func (l *Ledger) ReleaseInstance(instanceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releaseInstanceLocked(instanceID)
}

func (l *Ledger) Get(id string) (ResourceLease, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()
	lease := l.leases[id]
	if lease == nil {
		return ResourceLease{}, false
	}
	return cloneLease(lease), true
}

func (l *Ledger) GetByInstance(instanceID string) (ResourceLease, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()
	id := l.byInst[instanceID]
	if id == "" {
		return ResourceLease{}, false
	}
	lease := l.leases[id]
	if lease == nil {
		return ResourceLease{}, false
	}
	return cloneLease(lease), true
}

func (l *Ledger) Pending() []ResourceLease {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()
	out := make([]ResourceLease, 0, len(l.leases))
	for _, lease := range l.leases {
		if lease.State == LeasePending {
			out = append(out, cloneLease(lease))
		}
	}
	return out
}

func (l *Ledger) All() []ResourceLease {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()
	out := make([]ResourceLease, 0, len(l.leases))
	for _, lease := range l.leases {
		out = append(out, cloneLease(lease))
	}
	return out
}

func (l *Ledger) SweepExpired() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()
}

func (l *Ledger) sweepExpiredLocked() {
	now := l.now()
	for id, lease := range l.leases {
		if lease.State != LeasePending || lease.ExpiresAt.IsZero() || !lease.ExpiresAt.Before(now) {
			continue
		}
		l.releaseLocked(id)
	}
}

func (l *Ledger) releaseInstanceLocked(instanceID string) {
	if id := l.byInst[instanceID]; id != "" {
		l.releaseLocked(id)
	}
}

func (l *Ledger) releaseLocked(id string) {
	lease := l.leases[id]
	if lease == nil {
		return
	}
	delete(l.leases, id)
	if l.byInst[lease.InstanceID] == id {
		delete(l.byInst, lease.InstanceID)
	}
	for victim, owner := range l.claimed {
		if owner == id {
			delete(l.claimed, victim)
		}
	}
}

func (l *Ledger) usableCreditsLocked(requester string, credits []Credit) (map[string]bool, map[string]int64) {
	usable := map[string]bool{}
	bytesByDevice := map[string]int64{}
	for _, credit := range credits {
		victim := credit.InstanceID
		if victim == "" || victim == requester {
			continue
		}
		if owner := l.claimed[victim]; owner != "" {
			continue
		}
		usable[victim] = true
		reserved := int64(0)
		if existing, ok := l.leaseByInstanceLocked(victim); ok {
			for _, gpu := range existing.GPUs {
				bytesByDevice[gpu.DeviceID] += gpu.Bytes
				reserved += gpu.Bytes
			}
		}
		if extra := credit.Bytes - reserved; extra > 0 {
			bytesByDevice[""] += extra
		}
	}
	return usable, bytesByDevice
}

func (l *Ledger) leaseByInstanceLocked(instanceID string) (*ResourceLease, bool) {
	id := l.byInst[instanceID]
	if id == "" {
		return nil, false
	}
	lease := l.leases[id]
	if lease == nil {
		return nil, false
	}
	return lease, true
}

type deviceOccupancy struct {
	pending   int64
	committed int64
}

func (l *Ledger) occupancyLocked(ignoreInstance string, credit map[string]bool) map[string]deviceOccupancy {
	out := map[string]deviceOccupancy{}
	for _, lease := range l.leases {
		if lease.InstanceID == ignoreInstance || credit[lease.InstanceID] {
			continue
		}
		for _, gpu := range lease.GPUs {
			occ := out[gpu.DeviceID]
			if lease.State == LeasePending {
				occ.pending += gpu.Bytes
			} else {
				occ.committed += gpu.Bytes
			}
			out[gpu.DeviceID] = occ
		}
	}
	return out
}

func adjustSnapshot(snapshot hardware.Snapshot, occupancy map[string]deviceOccupancy, creditBytes map[string]int64) hardware.Snapshot {
	adjusted := snapshot
	if len(adjusted.GPUs) == 0 {
		return adjusted
	}
	adjusted.GPUs = append([]hardware.GPU(nil), snapshot.GPUs...)
	unassignedCredit := creditBytes[""]
	best := 0
	for i := range adjusted.GPUs {
		if adjusted.GPUs[i].FreeBytes > adjusted.GPUs[best].FreeBytes {
			best = i
		}
	}
	for i := range adjusted.GPUs {
		gpu := adjusted.GPUs[i]
		occ := occupancy[gpu.ID]
		credit := creditBytes[gpu.ID]
		if i == best {
			credit += unassignedCredit
		}
		managed := occ.pending + occ.committed
		if gpu.TotalBytes > 0 {
			unmanaged := gpu.UsedBytes - occ.committed - credit
			if unmanaged < 0 {
				unmanaged = 0
			}
			free := gpu.TotalBytes - managed - unmanaged
			if free < 0 {
				free = 0
			}
			gpu.FreeBytes = free
			gpu.UsedBytes = gpu.TotalBytes - free
		} else {
			free := gpu.FreeBytes - occ.pending - occ.committed + credit
			if free < 0 {
				free = 0
			}
			gpu.FreeBytes = free
		}
		adjusted.GPUs[i] = gpu
	}
	return adjusted
}

func reservationsFor(placement Placement, snapshot hardware.Snapshot, request PlacementRequest) []GPUReservation {
	if len(placement.Devices) == 0 {
		return nil
	}
	required := request.RequiredBytes
	if required < 0 {
		required = 0
	}
	if len(placement.Devices) == 1 {
		return []GPUReservation{{DeviceID: placement.Devices[0], Bytes: required}}
	}
	reserve := request.ReserveBytes
	if reserve <= 0 {
		reserve = defaultVRAMReserveBytes
	}
	byID := map[string]hardware.GPU{}
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = gpu
	}
	remaining := required
	out := make([]GPUReservation, 0, len(placement.Devices))
	for i, id := range placement.Devices {
		usable := usableVRAM(byID[id], reserve)
		take := usable
		if take > remaining {
			take = remaining
		}
		if take < 0 {
			take = 0
		}
		if i == len(placement.Devices)-1 && remaining > take {
			take = remaining
		}
		out = append(out, GPUReservation{DeviceID: id, Bytes: take})
		remaining -= take
		if remaining < 0 {
			remaining = 0
		}
	}
	return out
}

func cloneLease(lease *ResourceLease) ResourceLease {
	out := *lease
	if lease.GPUs != nil {
		out.GPUs = append([]GPUReservation(nil), lease.GPUs...)
	}
	if lease.Placement.Devices != nil {
		out.Placement.Devices = append([]string(nil), lease.Placement.Devices...)
	}
	return out
}

func newLeaseID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf[:])
}
