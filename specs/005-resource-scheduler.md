# 005 — Resource Scheduler

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how llamacpp-manager decides whether a model instance can be started on the local machine, how GPU/RAM resources are reserved, and how eligible running instances are selected for eviction when capacity is insufficient.

The scheduler is a decision engine. It does not spawn or stop processes directly.

## 2. Goals

The scheduler must:

- understand local CPU RAM and GPU resources;
- support NVIDIA and AMD GPUs;
- honor explicit GPU assignments;
- support automatic placement when assignment is not fixed;
- support manual tensor split configuration;
- estimate resource demand conservatively;
- protect Always-On models from normal eviction;
- use model priority and LRU/resource pressure when choosing eviction candidates;
- avoid overcommitting the same resources during concurrent starts;
- expose understandable scheduling decisions to the UI.

## 3. Non-goals for v1

The scheduler does not:

- schedule across remote machines;
- create/remove replicas automatically;
- dynamically move a running model between GPUs;
- preempt an active inference request merely to free VRAM under normal conditions;
- optimize electricity cost;
- predict model quality;
- guarantee perfect memory estimates for every llama.cpp build/model combination.

## 4. Inputs

A scheduling decision uses a coherent snapshot containing:

### Hardware

- total system RAM;
- currently available system RAM;
- GPU inventory;
- per-GPU total VRAM;
- per-GPU free/used VRAM;
- GPU utilization where available;
- device health/availability;
- runtime backend/device identifiers.

### Model

- artifact size and parsed GGUF metadata when available;
- model architecture/parameter count if known;
- effective context size;
- KV cache configuration;
- GPU layer/offload configuration;
- batch/ubatch/parallel settings relevant to memory use;
- model priority;
- Always-On flag;
- startup policy.

### Instance definition

- selected GPU devices or automatic mode;
- manual/automatic tensor split;
- enabled state;
- instance-specific placement constraints.

### Runtime

- running instances;
- observed per-process/GPU memory where measurable;
- active request counts;
- last-used time;
- lifecycle state;
- pending scheduler reservations.

## 5. Resource model

The scheduler uses a normalized resource representation.

Conceptually:

```text
SystemMemory:
  total
  available

GPUDevice:
  id
  vendor
  total_vram
  available_vram
  utilization
  healthy

InstanceDemand:
  estimated_ram
  estimated_vram_by_device_or_total
  uncertainty
```

The exact internal types are implementation details.

## 6. Resource estimates

Estimation should progressively improve as more information becomes available.

### Level 1 — file-size fallback

Before full GGUF metadata is available, file size gives a lower-quality estimate. Add safety headroom rather than treating disk size as exact runtime memory.

### Level 2 — GGUF metadata

Use parsed model metadata to estimate:

- weights;
- architecture overhead;
- context/KV cache;
- configured GPU offload;
- parallel slots;
- known llama.cpp memory-affecting settings.

### Level 3 — observed historical load

After a model has been successfully loaded, observed peak/steady memory may inform future estimates for the same artifact/config fingerprint.

Observed values must be tied to configuration and binary fingerprints; do not blindly reuse measurements after major configuration changes.

## 7. Safety margin

The scheduler must not target 100% reported free RAM/VRAM.

Use configurable or implementation-defined reserve margins for:

- operating system/manager memory;
- GPU driver/runtime overhead;
- llama.cpp allocation variance;
- temporary startup allocations.

The UI may show both raw hardware availability and scheduler-usable capacity.

## 8. Scheduling request

A lifecycle start asks the scheduler for a plan.

Possible outcomes:

- `PLACE` — resources available immediately;
- `PLACE_AFTER_EVICTION` — resources can be made available by stopping specific eligible instances;
- `WAIT` — temporary reservations/start operations may soon free/resolve capacity;
- `REJECT_INSUFFICIENT_RESOURCES`;
- `REJECT_INVALID_PLACEMENT`;
- `REJECT_HARDWARE_UNAVAILABLE`.

The decision includes human-readable rationale suitable for diagnostics/UI.

### 8.1 Delivery phase boundary

Phase 5 implements the policy and planning primitives needed for eviction: inference activity tracking, LRU/last-used state, idle unloading, eviction eligibility/ranking, resource estimates and an eviction-plan API. Phase 5 may calculate or preview which instances would be evicted, but it does **not** automatically execute resource-pressure eviction before starting another model.

The end-to-end `PLACE_AFTER_EVICTION` load path is a **Phase 7 — Hardware integration** requirement because it depends on real RAM/VRAM availability, per-device placement and refreshed hardware state.

When Phase 7 is implemented, a start that cannot fit directly must follow this sequence:

```text
request model B
-> calculate required capacity from model configuration
-> read current RAM/VRAM and placement state
-> choose eligible eviction victims
-> revalidate and drain/stop those victims
-> refresh resource state
-> start model B
```

Until Phase 7, lifecycle may expose an eviction plan for diagnostics/testing, but model startup must not claim that resource-pressure eviction has been executed automatically.

## 9. Reservations

Concurrent model starts must not all observe the same free VRAM and overcommit it.

Before lifecycle begins evictions/startup, the scheduler creates an in-memory reservation for expected resources.

Reservations:

- have an owner operation/instance;
- reduce available schedulable capacity;
- expire or are explicitly released on failure/cancellation;
- convert into observed runtime allocation after successful startup;
- are reconstructed conservatively after manager restart rather than persisted as active truth.

Scheduler reservation updates must be serialized enough to guarantee consistent placement decisions without locking unrelated inference traffic.

## 10. Explicit GPU assignment

If an instance definition selects specific GPUs, the scheduler must honor that selection.

If a configured device no longer exists or is unavailable:

- do not silently substitute another device;
- return an invalid/unavailable placement decision;
- show the unresolved assignment in the UI.

This protects manual multi-GPU configurations from changing meaning after device-order changes.

## 11. Automatic GPU placement

For automatic placement, choose compatible healthy devices using a deterministic score.

The algorithm should prefer:

- enough available VRAM without eviction;
- fewer devices when one device can efficiently fit the requested configuration, unless the user's effective llama.cpp settings indicate multi-GPU use;
- balanced use when multiple devices are required;
- avoiding highly utilized devices when comparable free alternatives exist.

Exact scoring is implementation-specific and should remain testable/deterministic given the same snapshot.

## 12. Tensor split

The UI supports:

- automatic split;
- manual split.

### Automatic

The scheduler/launch configuration may allow llama.cpp to choose or may generate an appropriate split based on available VRAM and supported flags.

### Manual

User-specified proportions/values are preserved and validated against selected GPUs.

The scheduler evaluates capacity according to the intended split. It must not silently alter a manual split just to make a model fit.

## 13. Priority

User-visible priorities:

- Low;
- Normal;
- High.

Internally these map to ordered weights.

Priority affects eviction preference and start competition. It does not automatically give one inference request latency priority inside an already-running llama.cpp worker.

## 14. Always-On protection

Always-On means at least one instance of the model should remain available.

Normal eviction rules must not select the final satisfying instance of an Always-On model.

If an Always-On model has multiple loaded instances, extra instances beyond the required minimum may be eligible for eviction if they are otherwise safe and manually configured policy allows them to stop.

If total Always-On desired state exceeds physical capacity:

- return/report unsatisfied desired state;
- avoid endless alternating evictions/restarts;
- expose which models cannot currently be satisfied and why.

A deterministic priority rule may decide which Always-On starts succeed, but it must not continuously churn.

### 14.1 Phase 7 product decision — separate eviction protection

Before implementing Phase 7 resource-pressure eviction, the implementation must explicitly ask the product owner/user to resolve whether **Always-On is the only user-facing protection from normal eviction** or whether a second, independent protection concept is needed.

The decision to request is:

1. **Always-On only** — the final Always-On instance is protected; every non-Always-On idle model is eligible for resource-pressure eviction according to priority/LRU rules.
2. **Separate protection while loaded** — keep a distinct setting such as `Pin while loaded` / `Protect from eviction`, allowing a model to remain non-Always-On (not proactively loaded/restarted) while still being excluded from normal resource-pressure eviction whenever it is loaded.

Do **not** infer this product decision from the Phase 5 `eviction_enabled` field. That field is provisional and may be removed or renamed. Phase 7 work must surface this question and record the chosen answer in this specification **before** finalizing the Phase 7 schema, UI or eviction-eligibility behavior.

## 15. Eviction eligibility

An instance is normally eligible for eviction only when:

- it is READY;
- it is not the final protected Always-On instance;
- it has no active inference requests;
- it is not already DRAINING/STOPPING;
- no management operation currently pins it;
- stopping it does not violate explicit placement/lifecycle constraints.

V1 should not forcibly evict an active generation for ordinary resource pressure.

If no idle eligible victim exists, the new model start may wait or fail depending on deadline/policy.

## 16. Eviction ordering

Combine priority, recency and resource benefit.

Recommended ordering principles:

1. lower-priority models before higher-priority models;
2. within the same priority, least recently used before recently used;
3. prefer a victim/set of victims that frees sufficient resources with minimal disruption;
4. avoid evicting multiple small models if one equally low-priority idle model can satisfy the requirement;
5. consider startup/load cost only as a secondary optimization after correctness.

A conceptual score may use:

```text
eviction desirability =
  priority penalty
+ idle age benefit
+ resource-recovery benefit
- protection/pinning penalties
```

Do not expose an unstable opaque number as the main UX. Present plain-language rationale.

## 17. Multiple victims

A start may require more than one eviction.

The scheduler should produce an ordered set whose combined released resources satisfy the estimated need plus safety margin.

Avoid combinatorial optimization complexity in v1. A deterministic greedy algorithm based on eligibility/order/resource benefit is acceptable if tests demonstrate reasonable behavior.

## 18. LRU definition

`last_used_at` should be based on meaningful inference activity, preferably the completion/end time of the most recent request, with active requests considered newer/pinned.

Manual start without any inference traffic should not forever outrank actually used models. The implementation may track `loaded_at` separately and use it as a fallback when `last_used_at` is null.

## 19. Start competition

If two unloaded models request startup concurrently and capacity can satisfy only one:

- reservations make the decision deterministic;
- higher-priority request should win when both compete before placement is committed;
- equal-priority requests may use FIFO/start-request time;
- once an expensive eviction/start plan is committed, avoid unnecessary cancellation merely because another equal request arrives.

The scheduler does not need a general-purpose job queue in v1, but placement operations need ordered coordination.

## 20. Interaction with lifecycle

The scheduler returns a plan; lifecycle executes it. **Execution of `PLACE_AFTER_EVICTION` before loading the requested model is introduced in Phase 7.** Phase 5 only establishes the decision/planning inputs and output.

For `PLACE_AFTER_EVICTION` in Phase 7:

1. scheduler creates reservation/plan;
2. lifecycle revalidates victim eligibility;
3. lifecycle drains/stops victims;
4. hardware snapshot is refreshed as needed;
5. lifecycle asks scheduler to confirm placement;
6. supervisor starts target instance;
7. reservation converts/releases based on outcome.

If a victim becomes active before drain starts, re-plan rather than violating the active-request rule.

## 21. Startup failure and resource release

If worker startup fails:

- release scheduler reservation;
- refresh observed hardware state;
- do not automatically restart evicted models unless their lifecycle policy (e.g. Always-On) requires reconciliation;
- preserve failure reason for the requesting model.

An eviction is a real lifecycle change, so users should understand that a failed new model load may leave previously idle models unloaded.

## 22. OOM handling

Despite estimates, llama.cpp startup may fail due to OOM.

Classify this separately when detectable.

On OOM:

- terminate failed worker;
- refresh hardware state;
- record actual/estimated mismatch;
- increase conservative estimate/headroom for that config fingerprint if possible;
- optionally attempt one re-plan with additional free capacity if request deadline and bounded retry policy permit;
- never enter infinite eviction/retry loops.

## 23. Hardware collectors

### NVIDIA

Collect at least:

- stable device identity;
- index/runtime-visible ID;
- name;
- total/free/used VRAM;
- utilization;
- relevant process memory when available.

### AMD

Collect an equivalent normalized set where ROCm/driver tools expose it.

Vendor collectors may use different mechanisms, but downstream scheduler logic consumes a common model.

Collector absence/failure must be visible. Unknown is not the same as zero free memory.

## 24. CPU-only mode

The scheduler must work without GPUs.

In CPU mode:

- evaluate system RAM;
- reject GPU-specific manual assignments;
- generate/validate effective llama.cpp configuration consistent with CPU execution;
- still apply priority, Always-On and idle eviction logic based on RAM/resource pressure.

## 25. Scheduler visibility in UI

For each model, the UI should be able to show:

- estimated RAM/VRAM requirement;
- selected/automatic GPUs;
- whether the model currently fits;
- why a start cannot proceed;
- which models would likely need eviction for a requested start, where safe to preview;
- recommendation confidence/estimate quality.

For running instances, show observed memory when available alongside estimates.

## 26. Metrics

Expose scheduler metrics including:

- placement requests;
- placement successes/failures;
- reservation counts;
- eviction count by reason/model priority;
- scheduling duration;
- estimate vs observed memory error where practical;
- unsatisfied Always-On model count.

Avoid device names or arbitrary error strings as uncontrolled metric labels.

## 27. Scheduler invariants

1. The scheduler never directly kills or starts a process.
2. Pending reservations reduce schedulable capacity.
3. Manual GPU assignment is never silently rewritten.
4. The final required Always-On instance is not a normal eviction victim.
5. Active inference requests protect an instance from normal eviction.
6. Unknown hardware telemetry causes conservative behavior.
7. Placement decisions include safety headroom.
8. Resource state is revalidated after evictions before launch.
9. Failed starts release reservations.
10. Scheduling cannot enter an unbounded eviction/start loop.

## 28. Acceptance criteria

Phase 5 tests must demonstrate the policy/planning behavior that does not require real hardware telemetry, including priority/LRU ordering, activity protection, Always-On protection and multi-victim planning.

Phase 7 tests must additionally demonstrate that a model start which requires resource-pressure eviction actually performs the complete pre-load sequence: calculate the deficit, select eligible victims, drain/stop them, refresh resource state, and only then start the requested model.

Across the completed scheduler implementation, tests must demonstrate:

- a model that fits available GPU memory receives a direct placement plan;
- two simultaneous starts cannot reserve the same VRAM twice;
- explicit GPU selection is honored;
- missing configured GPU causes a clear placement failure;
- manual tensor split is preserved;
- a low-priority idle model is selected before a high-priority idle model;
- older idle usage wins among equal-priority candidates;
- active instances are not normal eviction victims;
- the final Always-On instance is protected;
- an extra instance of an Always-On model can be considered separately from the protected minimum;
- insufficient capacity with no eligible victims fails cleanly;
- multi-victim eviction can free enough capacity;
- OOM startup failure releases reservations and records the mismatch;
- CPU-only scheduling works without GPU collectors;
- mutually impossible Always-On desired state is reported without endless churn.