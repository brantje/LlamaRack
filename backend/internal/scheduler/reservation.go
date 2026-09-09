package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const (
	LeasePending   = "pending"
	LeaseCommitted = "committed"

	ResourceOwnerInstance  = "instance"
	ResourceOwnerBenchmark = "benchmark"

	defaultLeaseTTL = 180 * time.Second
)

type GPUReservation struct {
	DeviceID string
	Bytes    int64
}

type ResourceOwner struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type ResourceLease struct {
	ID string `json:"id"`

	// Owner is the scheduler identity that owns this reservation. InstanceID is
	// retained for existing lifecycle callers and is populated only for Instance
	// owners. Jobs such as benchmarks use their own owner namespace so a run for
	// an already-loaded Instance cannot replace that Instance's worker lease.
	Owner      ResourceOwner `json:"owner"`
	InstanceID string        `json:"instance_id,omitempty"`

	Placement Placement        `json:"placement"`
	GPUs      []GPUReservation `json:"gpus,omitempty"`
	HostRAM   int64            `json:"host_ram"`
	State     string           `json:"state"`
	ExpiresAt time.Time        `json:"expires_at,omitempty"`
}

// Credit treats another Instance's reserved/estimated capacity as available to
// the acquiring start (resource-pressure eviction). A credited Instance cannot
// be credited to two in-flight starts at once.
//
// GPUs is the per-device vector that may be added to free VRAM. Scalar Bytes is
// retained for callers that only identify a victim; it is never applied to an
// arbitrary GPU.
type Credit struct {
	InstanceID string
	Bytes      int64
	GPUs       []GPUReservation
}

type AcquireRequest struct {
	// Owner is the preferred scheduler identity. InstanceID remains the legacy
	// spelling for Instance lifecycle callers; when Owner is empty it is treated
	// as ResourceOwner{Kind: "instance", ID: InstanceID}.
	Owner      ResourceOwner
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
	byOwner map[string]string
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
		byOwner: map[string]string{},
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
// when the placement fits, records a pending lease owned by the supplied owner.
// Legacy callers that only set InstanceID continue to own an Instance lease.
func (l *Ledger) Acquire(req AcquireRequest) (ResourceLease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()

	// Preserve the validation contract for legacy Instance-only callers while
	// leaving explicit resource-owner validation to normalizeOwner.
	if strings.TrimSpace(req.Owner.Kind) == "" && strings.TrimSpace(req.Owner.ID) == "" && strings.TrimSpace(req.InstanceID) == "" {
		return ResourceLease{}, errors.New("instance id is required")
	}
	owner, err := normalizeOwner(req.Owner, req.InstanceID)
	if err != nil {
		return ResourceLease{}, err
	}
	ownerKey := resourceOwnerKey(owner)
	l.releaseOwnerLocked(owner)

	requesterInstance := ""
	credits := req.Credits
	if owner.Kind == ResourceOwnerInstance {
		requesterInstance = owner.ID
	} else {
		// Credits are eviction semantics and only make sense for normal Instance
		// starts. Background jobs are deliberately non-preemptive by default.
		credits = nil
	}
	usableCredits, creditBytes := l.usableCreditsLocked(requesterInstance, credits)
	gpuOccupancy, hostOccupancy := l.occupancyLocked(ownerKey, usableCredits)
	adjusted := adjustSnapshot(req.Snapshot, gpuOccupancy, hostOccupancy, creditBytes)
	placement, err := PlanPlacement(adjusted, req.Placement)
	if err != nil {
		return ResourceLease{}, err
	}
	if placement.Fits && req.HostRAM > 0 && (adjusted.RAMTotalBytes > 0 || adjusted.RAMAvailableBytes > 0) && adjusted.RAMAvailableBytes < req.HostRAM {
		placement.Fits = false
	}
	if !placement.Fits {
		return ResourceLease{Owner: owner, Placement: placement}, nil
	}

	lease := &ResourceLease{
		ID:        l.newID(),
		Owner:     owner,
		Placement: placement,
		GPUs:      reservationsFor(placement, adjusted, req.Placement),
		HostRAM:   req.HostRAM,
		State:     LeasePending,
		ExpiresAt: l.now().Add(l.ttl),
	}
	if owner.Kind == ResourceOwnerInstance {
		lease.InstanceID = owner.ID
	}
	l.leases[lease.ID] = lease
	l.byOwner[ownerKey] = lease.ID
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

func (l *Ledger) CommitOwner(owner ResourceOwner) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	owner, err := normalizeOwner(owner, "")
	if err != nil {
		return err
	}
	id := l.byOwner[resourceOwnerKey(owner)]
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

func (l *Ledger) CommitInstance(instanceID string) error {
	if strings.TrimSpace(instanceID) == "" {
		return nil
	}
	return l.CommitOwner(ResourceOwner{Kind: ResourceOwnerInstance, ID: instanceID})
}

func (l *Ledger) Release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releaseLocked(id)
}

func (l *Ledger) ReleaseOwner(owner ResourceOwner) {
	owner, err := normalizeOwner(owner, "")
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releaseOwnerLocked(owner)
}

func (l *Ledger) ReleaseInstance(instanceID string) {
	if strings.TrimSpace(instanceID) == "" {
		return
	}
	l.ReleaseOwner(ResourceOwner{Kind: ResourceOwnerInstance, ID: instanceID})
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

func (l *Ledger) GetByOwner(owner ResourceOwner) (ResourceLease, bool) {
	owner, err := normalizeOwner(owner, "")
	if err != nil {
		return ResourceLease{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepExpiredLocked()
	id := l.byOwner[resourceOwnerKey(owner)]
	if id == "" {
		return ResourceLease{}, false
	}
	lease := l.leases[id]
	if lease == nil {
		return ResourceLease{}, false
	}
	return cloneLease(lease), true
}

func (l *Ledger) GetByInstance(instanceID string) (ResourceLease, bool) {
	if strings.TrimSpace(instanceID) == "" {
		return ResourceLease{}, false
	}
	return l.GetByOwner(ResourceOwner{Kind: ResourceOwnerInstance, ID: instanceID})
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

func (l *Ledger) releaseOwnerLocked(owner ResourceOwner) {
	if id := l.byOwner[resourceOwnerKey(owner)]; id != "" {
		l.releaseLocked(id)
	}
}

func (l *Ledger) releaseLocked(id string) {
	lease := l.leases[id]
	if lease == nil {
		return
	}
	delete(l.leases, id)
	key := resourceOwnerKey(lease.Owner)
	if l.byOwner[key] == id {
		delete(l.byOwner, key)
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
		if len(credit.GPUs) > 0 {
			for _, gpu := range credit.GPUs {
				id := strings.TrimSpace(gpu.DeviceID)
				if id == "" || gpu.Bytes <= 0 {
					continue
				}
				bytesByDevice[id] += gpu.Bytes
			}
			continue
		}
		if existing, ok := l.leaseByInstanceLocked(victim); ok {
			for _, gpu := range existing.GPUs {
				id := strings.TrimSpace(gpu.DeviceID)
				if id == "" || gpu.Bytes <= 0 {
					continue
				}
				bytesByDevice[id] += gpu.Bytes
			}
		}
	}
	return usable, bytesByDevice
}

func (l *Ledger) leaseByInstanceLocked(instanceID string) (*ResourceLease, bool) {
	id := l.byOwner[resourceOwnerKey(ResourceOwner{Kind: ResourceOwnerInstance, ID: instanceID})]
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

type hostOccupancy struct {
	pending   int64
	committed int64
}

func (l *Ledger) occupancyLocked(ignoreOwner string, credit map[string]bool) (map[string]deviceOccupancy, hostOccupancy) {
	out := map[string]deviceOccupancy{}
	var host hostOccupancy
	for _, lease := range l.leases {
		if resourceOwnerKey(lease.Owner) == ignoreOwner || (lease.InstanceID != "" && credit[lease.InstanceID]) {
			continue
		}
		if lease.State == LeasePending {
			host.pending += lease.HostRAM
		} else {
			host.committed += lease.HostRAM
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
	return out, host
}

func adjustSnapshot(snapshot hardware.Snapshot, occupancy map[string]deviceOccupancy, host hostOccupancy, creditBytes map[string]int64) hardware.Snapshot {
	adjusted := snapshot
	if adjusted.RAMTotalBytes > 0 {
		used := adjusted.RAMTotalBytes - adjusted.RAMAvailableBytes
		if used < 0 {
			used = 0
		}
		unmanaged := used - host.committed
		if unmanaged < 0 {
			unmanaged = 0
		}
		available := adjusted.RAMTotalBytes - host.pending - host.committed - unmanaged
		if available < 0 {
			available = 0
		}
		adjusted.RAMAvailableBytes = available
	} else if adjusted.RAMAvailableBytes > 0 {
		available := adjusted.RAMAvailableBytes - host.pending - host.committed
		if available < 0 {
			available = 0
		}
		adjusted.RAMAvailableBytes = available
	}
	if len(adjusted.GPUs) == 0 {
		return adjusted
	}
	adjusted.GPUs = append([]hardware.GPU(nil), snapshot.GPUs...)
	for i := range adjusted.GPUs {
		gpu := adjusted.GPUs[i]
		occ := occupancy[gpu.ID]
		credit := creditBytes[gpu.ID]
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

func normalizeOwner(owner ResourceOwner, legacyInstanceID string) (ResourceOwner, error) {
	owner.Kind = strings.ToLower(strings.TrimSpace(owner.Kind))
	owner.ID = strings.TrimSpace(owner.ID)
	legacyInstanceID = strings.TrimSpace(legacyInstanceID)
	if owner.Kind == "" && owner.ID == "" && legacyInstanceID != "" {
		owner = ResourceOwner{Kind: ResourceOwnerInstance, ID: legacyInstanceID}
	}
	if owner.ID == "" {
		return ResourceOwner{}, errors.New("resource lease owner id is required")
	}
	if owner.Kind != ResourceOwnerInstance && owner.Kind != ResourceOwnerBenchmark {
		return ResourceOwner{}, errors.New("resource lease owner kind must be instance or benchmark")
	}
	if legacyInstanceID != "" && (owner.Kind != ResourceOwnerInstance || owner.ID != legacyInstanceID) {
		return ResourceOwner{}, errors.New("resource lease owner conflicts with legacy instance id")
	}
	return owner, nil
}

func resourceOwnerKey(owner ResourceOwner) string {
	return owner.Kind + "\x00" + owner.ID
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
