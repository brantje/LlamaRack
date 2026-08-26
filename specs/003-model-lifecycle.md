# 003 — Model Lifecycle

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how configured models and their runtime instances move between lifecycle states, how autoloading and Always-On policy work, and how the manager recovers from crashes and restarts.

The lifecycle layer owns desired state and coordinates with the scheduler and process supervisor. It does not directly spawn processes and it does not choose request-routing targets.

## 2. Terminology

- **Model** — user-facing configured model with a public model ID.
- **Instance definition** — durable configured runtime slot for a model.
- **Runtime instance** — the currently observed `llama-server` process associated with an instance definition.
- **Desired state** — what policy/configuration says should exist.
- **Observed state** — what the manager currently sees from the operating system and worker health checks.
- **Autoload** — permission to load an unloaded model in response to an inference request.
- **Always On** — policy requiring at least one READY/starting instance to be maintained for the model.

## 3. Instance lifecycle states

The canonical runtime states are:

- `UNLOADED`
- `QUEUED`
- `STARTING`
- `LOADING`
- `READY`
- `DRAINING`
- `STOPPING`
- `FAILED`

### 3.1 UNLOADED

No managed worker process is active for the instance definition.

The instance may still be enabled and eligible for autoload.

### 3.2 QUEUED

A start has been requested but the scheduler has not yet committed resources or a start slot is waiting behind another lifecycle action.

QUEUED is useful to distinguish waiting for placement from a process that has actually begun startup.

### 3.3 STARTING

The supervisor has accepted a launch plan and is starting the child process. The process may exist but is not yet confirmed to be loading/serving.

### 3.4 LOADING

The child process is alive and model initialization is in progress, but readiness has not been confirmed.

If upstream llama.cpp does not expose a perfect distinction between STARTING and LOADING, the manager may infer the boundary from process/log/health behavior. The external state machine must still preserve the distinction when practical.

### 3.5 READY

The worker passed readiness checks and may receive inference traffic.

Only READY instances are eligible for normal routing.

### 3.6 DRAINING

The instance is no longer selected for new requests but one or more existing requests/streams may still be active.

DRAINING is used for graceful idle shutdown, user stop, controlled restart and some eviction flows.

### 3.7 STOPPING

No new requests are accepted and the supervisor is terminating the worker.

### 3.8 FAILED

The last requested lifecycle action failed or the process exited unexpectedly and policy has not yet successfully restored it.

FAILED must include a concise machine-readable reason plus human-readable summary, while full details remain available in instance logs.

## 4. Valid transition model

Primary transitions:

```text
UNLOADED -> QUEUED -> STARTING -> LOADING -> READY

READY -> DRAINING -> STOPPING -> UNLOADED

QUEUED   -> FAILED
STARTING -> FAILED
LOADING  -> FAILED
READY    -> FAILED     (unexpected worker exit)
DRAINING -> FAILED     (unexpected exit may still be recorded)
STOPPING -> UNLOADED
STOPPING -> FAILED     (unable to terminate cleanly / unknown process state)

FAILED -> QUEUED       (manual or policy retry)
FAILED -> UNLOADED     (clear/reset after confirming no process exists)
```

Implementation may contain internal substates, but the management API and UI should use this stable vocabulary.

## 5. Aggregate model status

A Model may have multiple instance definitions. Its displayed aggregate status is derived, not independently stored as truth.

Suggested precedence:

1. `READY` if at least one instance is READY;
2. `LOADING` if none is READY and any instance is STARTING/LOADING;
3. `QUEUED` if none above and any is QUEUED;
4. `DRAINING` if only draining/stopping activity remains;
5. `FAILED` if no usable/starting instance exists and one or more instances failed;
6. `UNLOADED` otherwise.

The UI should also show counts so aggregate state does not hide partial failures such as `1 ready / 1 failed`.

## 6. Start triggers

A start can be triggered by:

- user/operator action;
- an inference request to an unloaded model with autoload enabled;
- Always-On reconciliation;
- restart after configuration change;
- recovery policy after an unexpected worker exit.

All triggers enter the same lifecycle coordinator. There must not be separate ad-hoc spawn paths.

## 7. Autoload behavior

When an inference request resolves to a model with no READY instance:

1. If the model is disabled, fail without starting it.
2. If an instance is already STARTING/LOADING/QUEUED, attach the request as a waiter to that shared load operation.
3. If no load is in progress and autoload is disabled, return a model-unavailable error.
4. If autoload is enabled, request a start through the lifecycle coordinator.
5. Wait until a READY instance exists or the configured startup timeout expires.
6. Route the request once READY.

Multiple simultaneous requests must never independently launch duplicate workers merely because they all observed the model as unloaded.

The implementation therefore requires a per-model single-flight load operation.

## 8. Waiting request behavior

Each waiting request has its own client cancellation and deadline.

If one client disconnects:

- remove that waiter;
- do not cancel the shared model startup if another waiter exists;
- do not cancel startup if Always-On policy requires the model;
- optionally cancel startup if no waiter remains, Always-On is false, and policy explicitly permits startup cancellation. V1 may choose the simpler behavior of allowing the already-started load to finish.

The configured model startup timeout is the maximum normal wait for the model to become READY. Individual client deadlines may be shorter.

## 9. Always-On policy

`always_on = true` means the manager must maintain **at least one** usable instance for the enabled model.

A reconciliation loop periodically evaluates each Always-On model.

Desired invariant:

```text
enabled && always_on
=> at least one instance is READY, STARTING, LOADING or QUEUED
```

If no such instance exists, the lifecycle coordinator requests one.

Always-On does not require all manually defined instances to remain loaded. It guarantees a minimum of one in v1.

### 9.1 Manager startup

After initialization and hardware discovery, all enabled Always-On models are reconciled. They may start concurrently subject to scheduler/resource limits.

### 9.2 Worker crash

If the only READY instance for an Always-On model exits unexpectedly, the model immediately becomes unsatisfied and should be queued for restart according to retry/backoff policy.

### 9.3 Resource contention

Always-On instances are protected from normal eviction. If the system cannot satisfy all Always-On models simultaneously, the UI must expose the unsatisfied desired state and reason. The scheduler must not silently oscillate between mutually impossible Always-On models.

## 10. Idle unloading

Idle unloading applies only to models that are not required to remain loaded by Always-On policy.

A model is considered idle when:

- it has no active requests;
- it has no queued/waiting inference requests;
- no lifecycle operation requires it to stay loaded;
- `now - last_request_completed_or_activity_at >= effective_idle_timeout`.

When idle timeout is reached:

1. mark selected READY instance DRAINING;
2. stop routing new requests to it;
3. if an in-flight request appeared before the drain boundary, wait for it to finish subject to drain timeout;
4. transition to STOPPING;
5. terminate the worker;
6. transition to UNLOADED.

If a new request arrives while a model is DRAINING but before process termination, v1 should use deterministic behavior. Preferred behavior:

- cancel the idle shutdown if safe and return the instance to READY before STOPPING begins;
- once STOPPING begins, let shutdown complete and use normal autoload to start again.

This avoids routing to a process already being terminated.

## 11. Manual stop behavior

An authorized user may stop a model or instance.

For a non-Always-On model, stop means graceful drain then unload.

For an Always-On model, the UI must make the policy conflict explicit. Recommended behavior:

- `Stop instance` may stop one instance if another satisfies Always-On;
- attempting to stop the final instance requires either disabling Always-On first or using an explicit temporary-stop operation if such a feature is later added.

V1 should prefer clear policy enforcement over hidden immediate restarts that make the Stop button appear broken.

## 12. Configuration changes

Each running instance records the effective launch configuration fingerprint.

When model configuration or relevant binary/instance placement configuration changes:

- compute a new desired fingerprint;
- if it differs from a running instance fingerprint, mark `restart_required`;
- do not pretend the running process has adopted settings it cannot change dynamically.

The user may choose a controlled restart, and some changes may support an explicit `save and restart` action.

For an Always-On model with only one instance, a restart temporarily violates READY availability while the model reloads. V1 does not promise zero-downtime restarts.

## 13. Controlled restart

A controlled restart is:

```text
READY
 -> DRAINING
 -> STOPPING
 -> UNLOADED
 -> QUEUED
 -> STARTING
 -> LOADING
 -> READY
```

The lifecycle coordinator must serialize restart with simultaneous manual/autoload/Always-On actions.

## 14. Unexpected process exit

When the supervisor observes a child exit:

1. remove it from routing immediately;
2. capture exit code/signal and final log context;
3. update observed state to FAILED unless the exit was expected as part of STOPPING;
4. clear ephemeral PID/port state;
5. inform lifecycle reconciliation.

Restart policy:

- Always-On model: retry automatically with bounded exponential backoff;
- model with waiting autoload requests: retry only within a bounded startup attempt policy;
- ordinary non-Always-On idle model: remain FAILED/UNLOADED until manual start or later autoload, depending on failure classification.

Do not create infinite tight crash loops.

## 15. Startup readiness

A process being alive is not sufficient for READY.

Readiness requires a successful llama-server readiness/health condition appropriate to the installed version.

Startup timeout begins when the start operation is accepted or process launch begins; choose one definition consistently and expose it in API documentation.

On timeout:

- stop/kill the incomplete worker if still running;
- transition to FAILED;
- record `startup_timeout`;
- fail waiting inference requests.

## 16. Health after readiness

READY workers require ongoing health observation.

A transient failed health probe should not immediately kill a healthy long-running generation. Use a configurable or hard-coded bounded failure threshold.

If the worker is unhealthy beyond threshold:

- remove from routing;
- transition through an unhealthy/FAILED internal path;
- allow lifecycle policy to restart if required.

Public state may remain FAILED rather than introduce another top-level `UNHEALTHY` state in v1.

## 17. Drain behavior

DRAINING must stop new routing immediately.

Existing streaming and non-streaming requests are allowed to finish up to a drain timeout.

If drain timeout expires:

- cancel/terminate outstanding proxied requests where possible;
- proceed with worker shutdown;
- clients receive connection/error behavior appropriate to whether response streaming had already started.

No new request may be intentionally sent to a DRAINING worker.

## 18. Shutdown behavior

On manager shutdown:

1. stop accepting new management mutations and new inference requests where practical;
2. mark managed READY workers DRAINING;
3. allow bounded request completion;
4. ask supervisor to terminate all managed workers;
5. persist durable state;
6. exit only after workers are gone or hard shutdown deadline is reached.

The manager should not intentionally leave orphaned `llama-server` processes.

## 19. Manager restart recovery

Persisted runtime fields such as PID and READY state are never trusted as live state after restart.

On startup:

- treat previous runtime records as stale diagnostics;
- verify/clean up manager-owned orphan processes if a safe ownership mechanism is available;
- reconstruct observed state from actual processes started by the new manager session;
- mark normal models UNLOADED;
- reconcile Always-On models back toward desired state.

The design should include a manager/worker ownership token or equivalent launch marker so unrelated user-run llama-server processes are never killed.

## 20. Resource eviction lifecycle

When the scheduler selects an instance for eviction:

- lifecycle confirms the instance is still eligible;
- mark it DRAINING;
- remove it from routing;
- wait for active requests to finish within policy;
- stop it;
- release the scheduler reservation;
- continue starting the requesting model.

Scheduling decisions must be revalidated because resource state may change between planning and execution.

## 21. Failure classifications

At minimum distinguish:

- `invalid_configuration`;
- `artifact_missing`;
- `artifact_invalid`;
- `insufficient_resources`;
- `startup_timeout`;
- `worker_crash`;
- `health_check_failed`;
- `port_allocation_failed`;
- `binary_missing`;
- `unsupported_option`;
- `permission_error`;
- `unknown`.

The UI should display the concise classification and link/show relevant instance logs.

## 22. API operation semantics

Lifecycle mutations should return operation/state information rather than imply immediate readiness.

Examples:

- Start request may return current state `QUEUED`/`STARTING`.
- Stop request may return `DRAINING`/`STOPPING`.
- Repeated Start on an already starting model is idempotent and attaches to the same desired operation.
- Repeated Stop on an unloaded model is idempotently successful.

Inference autoload waits internally because compatibility clients expect the inference request itself to either execute or fail; management API calls need not block through full model load unless an explicit wait option is added.

## 23. Timing defaults

Exact defaults remain implementation/product settings, but the following must be configurable at least globally and overridable where specified:

- model startup timeout;
- idle unload timeout;
- drain timeout;
- health failure threshold/interval if exposed;
- crash restart backoff policy.

Avoid per-model knobs for every internal timer unless there is user value.

## 24. Lifecycle invariants

1. Only READY instances receive new inference requests.
2. At most one start/load operation per model is active unless deliberately starting a second manual instance.
3. An Always-On model is continuously reconciled toward at least one usable instance.
4. Idle unload never stops an instance with active requests.
5. A worker PID/port is cleared when its process exits.
6. Unexpected exits never remain displayed as READY.
7. Configuration changes never silently claim to affect already-running immutable worker options.
8. Manual stop cannot silently fight Always-On policy.
9. Eviction goes through DRAINING before normal termination unless emergency process failure makes that impossible.
10. A manager restart never assumes old READY state is still valid.

## 25. Acceptance criteria

Lifecycle implementation is complete when tests demonstrate:

- manual start from UNLOADED to READY;
- manual graceful stop from READY to UNLOADED;
- autoload from an inference request;
- 20 simultaneous requests to one unloaded model cause one startup, not 20;
- one waiting client disconnect does not abort startup required by other clients;
- startup timeout produces FAILED and cleans up the child process;
- an Always-On model starts after manager boot;
- an Always-On worker crash causes bounded automatic restart;
- an idle non-Always-On model unloads after timeout;
- an active generation prevents idle unload;
- a new request during early drain either safely cancels the idle stop or waits through a deterministic reload path;
- configuration fingerprint change is reported as restart-required;
- controlled restart does not route new requests to the draining worker;
- manager restart does not trust stale PID/READY state;
- resource eviction drains an eligible model before starting the requesting model.