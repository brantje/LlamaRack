# 005 — Resource Scheduler

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how `llamacpp-manager` decides whether a configured Instance can start on the local machine, how RAM/VRAM is reserved, how GPU placement is selected, and how eligible running Instances are chosen for resource-pressure eviction.

The scheduler is a decision engine. It never directly starts or stops processes.

## 2. Ownership

Scheduling operates on **Instances**.

A Model contributes static/resource-relevant inputs such as:

- artifact size;
- architecture/parameter metadata;
- Model-level llama.cpp defaults.

An Instance contributes runtime policy and effective configuration such as:

- `instance.id`;
- priority;
- Always On;
- `eviction_enabled`;
- effective context/KV/batch/offload configuration;
- GPU placement mode;
- selected GPUs;
- tensor split configuration;
- last-used/activity state.

Lifecycle/scheduler policy must not be stored on the Model.

## 3. Goals

The scheduler must:

- understand local CPU RAM and GPU resources;
- support NVIDIA and AMD GPUs;
- honor manual GPU assignments;
- support automatic placement;
- prefer one GPU when the Instance safely fits on one device;
- support automatic/manual tensor split;
- estimate memory conservatively;
- treat Always On as desired lifecycle state rather than eviction protection;
- use `eviction_enabled` as the source of truth for normal resource-pressure protection;
- use Instance priority and LRU/resource benefit for eviction ordering;
- avoid overcommitting resources during concurrent starts;
- expose understandable scheduling decisions;
- avoid eviction/restart oscillation when an eviction-enabled Always-On Instance is displaced.

## 4. Non-goals for v1

- remote-machine scheduling;
- automatic replica scaling;
- live GPU migration of a running worker;
- preempting active inference for ordinary resource pressure;
- perfect memory prediction;
- user-facing tuning of every scoring coefficient.

## 5. Inputs

### Hardware

- system RAM total/available;
- GPU inventory;
- per-GPU total/free/used VRAM;
- utilization where available;
- stable device identity;
- device health/availability;
- backend/runtime-visible indices.

### Model

- artifact identity/size;
- GGUF metadata where available;
- architecture/parameter count;
- inherited Model llama.cpp values.

### Instance

- `instance.id`;
- effective Global + Model + Instance llama.cpp configuration;
- priority;
- Always On;
- `eviction_enabled`;
- automatic/manual placement;
- selected GPU IDs;
- automatic/manual tensor split;
- enabled state;
- startup/resource constraints.

### Runtime

- running Instances;
- observed RAM/VRAM where measurable;
- active/queued request counts;
- last-used time;
- lifecycle state;
- pending reservations;
- resource-pressure-blocked desired state where applicable.

## 6. Resource estimation

Estimate demand progressively:

1. file-size fallback plus headroom;
2. GGUF metadata + context/KV/runtime settings;
3. observed memory for the same artifact/config/binary fingerprint.

Never treat disk file size as exact runtime memory.

Observed historical values are only reusable when tied to compatible configuration/binary fingerprints.

## 7. Safety margin

Do not target 100% reported free RAM/VRAM.

Reserve headroom for:

- OS/manager memory;
- GPU driver/runtime overhead;
- llama.cpp allocation variance;
- startup/transient allocation spikes.

The UI may show both raw free memory and scheduler-usable memory.

## 8. Scheduling result

Possible outcomes:

- `PLACE`;
- `PLACE_AFTER_EVICTION`;
- `WAIT`;
- `REJECT_INSUFFICIENT_RESOURCES`;
- `REJECT_INVALID_PLACEMENT`;
- `REJECT_HARDWARE_UNAVAILABLE`.

Results include human-readable rationale.

## 9. Delivery phase boundary

Phase 5 provides policy/planning primitives: activity tracking, LRU state, idle unload, eviction eligibility/ranking, estimates and an eviction-plan API.

Phase 5.5 moves those policies onto durable Instances and establishes the Instance control plane.

Actual hardware-aware execution of `PLACE_AFTER_EVICTION` remains a **Phase 7 — Hardware integration** requirement.

Phase 7 load flow:

```text
request Instance
-> calculate effective Instance demand
-> read current RAM/VRAM + reservations
-> choose placement
-> if insufficient, choose eligible victim Instances
-> revalidate victims
-> drain/stop victims
-> refresh hardware state
-> confirm placement
-> start requested Instance
```

Before Phase 7, code may calculate/preview eviction plans but must not claim real pre-load VRAM eviction has been executed.

## 10. Reservations

Concurrent starts must not all consume the same apparent free capacity.

Reservations:

- belong to one start operation/Instance;
- reduce schedulable capacity;
- expire/release on failure/cancellation;
- convert to observed allocation after successful startup;
- are reconstructed conservatively after manager restart rather than persisted as live truth.

Resource-pressure-blocked Always-On reconciliation must also respect pending/committed starts. It must not reclaim capacity during the gap between victim stop and requester startup.

## 11. Explicit GPU assignment

Manual placement must be honored exactly.

If a configured GPU is missing/unavailable:

- do not silently substitute another device;
- return invalid/unavailable placement;
- preserve configuration for user correction.

## 12. Automatic GPU placement

Automatic placement is manager-owned.

Default policy is **single-GPU first**:

1. calculate complete estimated VRAM demand plus safety margin;
2. evaluate each healthy compatible GPU individually;
3. if any one GPU fits, choose exactly one GPU;
4. generate explicit llama.cpp placement so the worker stays on that device;
5. only consider multi-GPU if no single GPU safely fits;
6. when multi-GPU is needed, use the minimum practical number of devices and derive a split from usable VRAM/effective configuration.

The manager must not expose all GPUs and let llama.cpp spread across them by default when one GPU can hold the Instance.

Manual user configuration can intentionally request multi-GPU behavior.

## 13. Tensor split

### Automatic

Automatic tensor split follows the scheduler-selected device set.

- one selected GPU => no multi-GPU split;
- multiple selected GPUs => generate a calculated split.

### Manual

User-provided split values are validated against selected GPUs and are not silently changed just to make an Instance fit.

## 14. Priority

User-visible priority:

- Low;
- Normal;
- High.

Priority affects start competition and eviction ordering for **Instances**.

It does not imply per-request priority inside a running llama.cpp worker.

## 15. Always-On and eviction protection

These are independent Instance properties.

### Always On

`always_on=true` means that exact Instance is reconciled toward running/READY state whenever resources permit, except during session-local manual-stop suppression.

Always On does **not** make the Instance ineligible for normal resource-pressure eviction.

### Resource-pressure eviction

`eviction_enabled` is the source of truth for normal resource-pressure protection:

- `eviction_enabled=true` — the Instance may be selected when all ordinary victim rules are satisfied;
- `eviction_enabled=false` — the loaded Instance is protected from normal resource-pressure eviction.

Supported policy matrix:

| Always On | Allow resource-pressure eviction | Meaning |
| --- | --- | --- |
| false | true | Start manually/on demand; may be evicted when eligible. |
| false | false | Start manually/on demand; protected from normal resource-pressure eviction while loaded. |
| true | true | Keep desired-running whenever resources permit; may be temporarily evicted and then reconciled non-preemptively. |
| true | false | Keep desired-running and protect from normal resource-pressure eviction. |

Autoload remains independent from both properties.

## 16. Eviction eligibility

An Instance is normally eligible only when:

- READY;
- `eviction_enabled=true`;
- no active inference requests;
- not already DRAINING/STOPPING;
- not pinned by another management operation;
- stopping it does not violate another explicit hard constraint.

`always_on` is **not** an eviction-eligibility exclusion and does not alter ranking. If an Always-On Instance is otherwise eligible, it participates in the same priority/LRU/resource-benefit ordering as any other victim.

V1 does not forcibly evict active generation for ordinary pressure.

## 17. Eviction ordering

Combine:

1. Instance priority — lower before higher;
2. LRU/last-used age;
3. resource benefit;
4. disruption/minimal victim count.

Prefer one suitable victim over several equivalent smaller victims when practical.

A deterministic greedy algorithm is acceptable in v1.

Always On does not add a ranking bonus or penalty. Its effect is lifecycle reconciliation after eviction, not victim ordering.

## 18. Multiple victims

A placement may require multiple victims.

The ordered victim set must collectively free sufficient resources plus safety margin.

Revalidate each victim immediately before drain because runtime activity can change after planning.

## 19. LRU definition

`last_used_at` is based on meaningful inference activity, preferably request completion/end time.

Active requests are treated as pinned/newest.

A manually started but unused Instance can use `loaded_at` as fallback when `last_used_at` is null.

## 20. Start competition

If two stopped Instances request start and only one fits:

- reservations make the choice deterministic;
- higher Instance priority wins when competition exists before commitment;
- equal priority can use FIFO/start-request time;
- avoid canceling a committed expensive plan merely because another equal request appears.

An Always-On victim that was evicted for a committed start must not immediately compete to reclaim the just-freed capacity. Its automatic recovery is temporarily non-preemptive.

## 21. Manual launch warning

A user-initiated Launch that may require eviction must show a confirmation explaining that other idle Instances may be stopped automatically according to configured policy.

The user does **not** manually select victim Instances.

After confirmation, scheduler policy chooses victims.

Inference-triggered autoload cannot wait for an interactive confirmation and proceeds according to policy automatically.

## 22. Interaction with lifecycle

Scheduler returns a plan; lifecycle executes it.

For Phase 7 `PLACE_AFTER_EVICTION`:

1. scheduler reserves/commits capacity and returns target placement + victims;
2. lifecycle revalidates victim eligibility;
3. lifecycle marks any Always-On victim as desired-running but `resource_pressure`-blocked;
4. lifecycle drains/stops victims;
5. hardware state is refreshed;
6. scheduler confirms the target placement;
7. supervisor starts the requested Instance using explicit placement;
8. reservation converts/releases based on outcome.

If a victim becomes active before drain, re-plan.

For an evicted Always-On victim, normal Always-On reconciliation must preserve desired state but use a no-eviction retry while `resource_pressure`-blocked. If current capacity is still insufficient, it remains unloaded and blocked. Once it fits without displacing another Instance, lifecycle starts it and clears the block.

An explicit user Launch or targeted inference request may override this temporary non-preemptive block and use normal scheduling policy.

## 23. Startup failure

If requested Instance startup fails:

- release reservation;
- refresh hardware state;
- keep the Instance configured;
- allow evicted Instances to follow their own lifecycle policy;
- preserve failure reason for the requested Instance.

An evicted Always-On Instance may therefore return once capacity is genuinely available, but its automatic retry must not cause an immediate eviction/restart loop.

A failed new start may leave ordinary evicted idle Instances stopped.

## 24. OOM handling

When OOM is detectable:

- terminate failed worker;
- refresh hardware state;
- record estimate mismatch for the effective fingerprint;
- increase conservative headroom where practical;
- optionally perform one bounded re-plan if deadline/policy permits;
- never loop indefinitely.

## 25. Hardware collectors

### NVIDIA

Collect stable ID, runtime index, name, total/free/used VRAM, utilization and relevant process memory where available.

### AMD

Provide equivalent normalized data where ROCm/driver tools permit.

Collector failure must be represented as unknown/unavailable, not zero free memory.

## 26. CPU-only mode

Without GPUs:

- schedule against system RAM;
- reject GPU-specific manual assignments;
- generate CPU-compatible llama.cpp configuration;
- retain Instance priority/Always-On/eviction behavior for RAM pressure.

## 27. Scheduler UI visibility

The Instance control plane should eventually expose:

- estimated RAM/VRAM;
- automatic/manual placement;
- selected GPU(s);
- whether automatic placement chose one GPU or multi-GPU;
- fit/cannot-fit reason;
- Always-On desired-state policy;
- resource-pressure eviction protection state;
- possible eviction consequence for manual Launch;
- observed resource usage where available.

The configuration copy must make the independence explicit:

- **Always On** — Keep this Instance running whenever resources permit.
- **Allow resource-pressure eviction** — Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance.

Do not put these operational fields on the `/models` table.

## 28. Metrics

Expose metrics such as:

- placement requests/success/failure;
- reservations;
- eviction count by Instance priority/reason;
- scheduling duration;
- estimate vs observed error;
- unsatisfied/resource-pressure-blocked Always-On Instance count.

Avoid uncontrolled high-cardinality labels.

## 29. Invariants

1. Scheduler operates on Instances.
2. Scheduler never starts/kills a process directly.
3. Pending reservations reduce schedulable capacity.
4. Manual GPU assignment is never silently rewritten.
5. Automatic placement is single-GPU first.
6. Always On is desired lifecycle state, not normal eviction protection.
7. `eviction_enabled=false` protects that exact Instance regardless of Always On.
8. `eviction_enabled=true` allows an otherwise eligible Always-On Instance to be selected and ranked normally.
9. Active requests protect an Instance from normal eviction.
10. An evicted Always-On Instance does not immediately preempt another Instance during automatic reconciliation.
11. Eviction execution before target load is a Phase 7 responsibility.
12. Model rows do not own scheduler/lifecycle policy.

## 30. Acceptance criteria

Tests/spec behavior demonstrate:

- two sibling Instances can have different priorities/eviction policies;
- all four `always_on` × `eviction_enabled` combinations behave independently;
- a non-Always-On protected Instance is not selected as a victim;
- an eviction-disabled Always-On Instance is not selected as a victim;
- an eviction-enabled Always-On Instance can be selected as an idle victim;
- Always On does not alter victim ranking;
- active requests pin an Instance;
- lower-priority idle Instances are preferred victims;
- an evicted Always-On Instance remains desired-running but resource-pressure-blocked;
- automatic reconciliation of a resource-pressure-blocked Always-On Instance does not evict the requester that displaced it;
- the blocked Instance starts automatically when sufficient uncommitted capacity returns;
- manual-stop suppression remains separate from resource-pressure blocking;
- automatic placement chooses one GPU when one safely fits;
- multi-GPU is considered only when needed or manually requested;
- missing manually assigned GPUs fail safely;
- reservations prevent concurrent overcommit;
- manual Launch warns about possible automatic eviction;
- inference-triggered autoload follows policy without interactive confirmation;
- Phase 7 executes drain/stop/refresh/start for real `PLACE_AFTER_EVICTION` flows.
