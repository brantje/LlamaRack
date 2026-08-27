# 003 — Instance Lifecycle

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines lifecycle behavior for durable configured **Instances**.

A Model is configuration and artifact metadata. An Instance is the unit that starts, stops, autoloads, reconciles, drains and fails.

## 2. Terminology

- **Model** — registered management-plane model and reusable llama.cpp defaults.
- **Instance** — durable configured `llama-server` process definition.
- **Runtime Instance** — currently observed child process for an Instance.
- **Desired state** — what Instance policy says should exist.
- **Observed state** — what the manager currently observes.
- **Autoload on request** — permission for an inference request targeting a stopped Instance to start that exact Instance.
- **Always On** — policy requiring that exact Instance to be continuously reconciled toward running/ready state whenever resources permit, except during explicit temporary manual-stop suppression. It does not itself grant resource-pressure eviction protection.
- **Resource-pressure block** — session-local lifecycle state indicating that an Always-On Instance still has desired state READY but is temporarily unloaded because capacity was committed elsewhere.

## 3. Instance identity

Lifecycle operations always use `instance.id`.

`instance.id` is the slug derived from Instance name and is also the OpenAI `model` value.

Example:

```text
Instance name: Qwen Coding 32B
instance.id:   qwen-coding-32b
```

An inference request for `qwen-coding-32b` may only start/use that exact Instance. It must not silently switch to another Instance referencing the same Model.

## 4. Lifecycle states

Canonical states:

- `UNLOADED`
- `QUEUED`
- `STARTING`
- `LOADING`
- `READY`
- `DRAINING`
- `STOPPING`
- `FAILED`

Primary transitions:

```text
UNLOADED -> QUEUED -> STARTING -> LOADING -> READY

READY -> DRAINING -> STOPPING -> UNLOADED

QUEUED   -> FAILED
STARTING -> FAILED
LOADING  -> FAILED
READY    -> FAILED
DRAINING -> FAILED
STOPPING -> UNLOADED | FAILED

FAILED -> QUEUED | UNLOADED
```

Only READY Instances receive new inference requests.

## 5. Start triggers

An Instance start can be triggered by:

- user Launch;
- inference request targeting a stopped autoload-enabled Instance;
- Always-On reconciliation;
- controlled restart after configuration change;
- crash-recovery policy when Always On or waiting inference requires it.

Every trigger enters the same lifecycle coordinator.

## 6. Per-Instance single-flight startup

Concurrent requests targeting the same stopped Instance must share one startup operation.

Example:

```text
20 requests -> model="qwen-coding"
              |
              v
        one Instance start
              |
              v
       all waiters continue
```

A request targeting a sibling Instance uses a separate lifecycle operation.

## 7. Autoload on request

When inference resolves `model` to an Instance:

1. If the Instance does not exist, return model-not-found.
2. If READY, route immediately.
3. If QUEUED/STARTING/LOADING, join the existing startup wait.
4. If stopped and Autoload is disabled, return model-unavailable.
5. If stopped and Autoload is enabled, request startup for that exact Instance.
6. Wait until READY or the effective request/startup deadline expires.
7. Proxy only to that Instance.

Do not search sibling Instances as substitutes.

## 8. Waiting request behavior

Each waiter retains its own cancellation/deadline.

If one caller disconnects:

- remove only that waiter;
- keep startup alive for other waiters;
- keep startup alive if Always On requires the Instance;
- cancellation of all waiters may allow startup cancellation only if product policy explicitly permits it.

## 9. Always-On policy

`instance.always_on = true` means the manager reconciles that exact Instance toward a usable state whenever resources permit.

Desired invariant when not manually suppressed or temporarily resource-blocked:

```text
enabled && always_on
=> state in READY/LOADING/STARTING/QUEUED
```

Always On no longer means “at least one Instance for this Model.” Every Instance carries its own policy.

Always On is a desired-lifecycle policy. It is independent from `eviction_enabled`, which alone controls normal resource-pressure eviction protection.

### 9.1 Manager startup

After startup recovery and hardware initialization, reconcile every enabled Always-On Instance.

### 9.2 Worker crash

If an Always-On Instance crashes, retry with bounded backoff unless manually suppressed or a permanent configuration/hardware failure blocks it.

### 9.3 Resource contention

An Always-On Instance may be selected for normal resource-pressure eviction when `eviction_enabled=true` and the ordinary victim-eligibility rules are satisfied.

When that happens:

```text
Always-On + eviction_enabled=true + READY
-> selected as victim
-> stop/unload
-> desired state remains READY
-> resource-pressure block = resource_pressure
```

The resource-pressure block is **not** a manual stop. Automatic reconciliation must not immediately evict another Instance merely to restore the evicted Always-On Instance. While the block is active, automatic retry is non-preemptive: it starts the Instance only when current capacity can satisfy it without resource-pressure eviction.

When sufficient capacity returns, reconciliation clears the block after the Instance successfully starts. An explicit user Launch or a targeted inference request may clear/override the block and use the normal scheduler policy, including eviction when permitted.

If `eviction_enabled=false`, Always On plus eviction protection means the Instance is both continuously desired and protected from normal resource-pressure eviction.

## 10. Manual stop of Always-On Instances

Users must be able to intentionally stop an Always-On Instance without watching it immediately restart.

When the user manually presses Stop:

```text
Always-On Instance
-> graceful stop
-> UNLOADED
-> session-local manual-stop suppression = true
```

While suppressed, normal Always-On reconciliation does not restart it.

Suppression clears when:

- the user explicitly presses Launch;
- an inference request targets that Instance and needs it;
- the manager restarts.

Suppression is session-local and is not durable desired configuration.

Manual-stop suppression and a resource-pressure block are separate lifecycle reasons. A user Stop replaces any resource-pressure block with manual-stop suppression.

## 11. Idle unloading

Idle unloading, when enabled, is Instance-specific and only applies when the Instance is not actively required by Always On or another lifecycle operation.

An Instance is idle when:

- no active requests;
- no queued/waiting inference requests;
- no lifecycle operation pins it;
- idle timeout elapsed.

Then:

```text
READY
-> DRAINING
-> STOPPING
-> UNLOADED
```

A new request during early drain may cancel the idle stop if safe. Once STOPPING starts, finish shutdown and use normal autoload if required.

## 12. Stop, Kill and Restart

### Stop

Graceful:

```text
READY -> DRAINING -> STOPPING -> UNLOADED
```

### Kill

Immediate explicit process termination for operational recovery. It does not promise graceful request completion.

### Restart

Controlled:

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

Lifecycle operations for one Instance must serialize safely.

## 13. Editing a running Instance

Runtime-affecting Instance edits do not use the normal long-lived “save and restart later” path.

Before saving, show confirmation that the Instance will restart and may temporarily interrupt availability/active work.

On confirmation:

1. persist desired configuration;
2. drain;
3. stop;
4. start with new effective configuration;
5. become READY or FAILED.

### 13.1 Instance rename

Renaming changes the slug-derived `instance.id` and therefore changes the OpenAI `model` identifier.

The UI must show a separate API-breaking-change warning before applying the rename.

If the Instance is running, the rename/update workflow must keep durable references consistent and restart if required by implementation identity binding.

No hidden old-ID alias is retained in v1.

## 14. Model/global configuration changes

Model/global llama.cpp defaults may affect multiple Instances through inheritance.

For affected running Instances:

- compute new desired fingerprints;
- never claim the running worker already adopted immutable changed values;
- expose restart-required state until a controlled restart is performed.

The automatic restart-on-save rule applies specifically to direct Instance edits. Broad global/Model changes may use explicit impact/restart workflows.

## 15. Unexpected process exit

On child exit:

1. immediately remove the Instance from READY routing;
2. capture exit code/signal/log context;
3. mark FAILED unless exit was expected during STOPPING;
4. clear PID/private port;
5. evaluate Instance policy.

Restart behavior:

- Always-On Instance: bounded automatic retry;
- Instance with active autoload waiters: bounded retry within request/startup policy;
- ordinary non-Always-On Instance: remain FAILED/UNLOADED until manual Launch or a later autoload request.

Never create tight crash loops.

## 16. Startup readiness and timeout

A live process is not enough for READY.

READY requires successful worker readiness/health criteria appropriate to the installed llama.cpp build.

On startup timeout:

- terminate incomplete worker;
- transition FAILED;
- record `startup_timeout`;
- fail waiting inference requests.

## 17. Health after readiness

READY workers require ongoing health checks.

Transient probe failure should not immediately kill a long generation. Use bounded failure thresholds.

Persistent unhealthy state removes the Instance from routing and allows lifecycle policy to decide restart.

## 18. Drain behavior

DRAINING:

- immediately rejects new routing to that Instance;
- allows existing requests to finish up to drain timeout;
- after timeout, terminates outstanding work as needed and proceeds to STOPPING.

No new request is intentionally sent to a DRAINING Instance.

## 19. Manager restart recovery

Persisted PID/port/READY values are stale after restart unless re-observed.

On startup:

- discard stale liveness assumptions;
- safely clean manager-owned orphan workers where ownership can be proven;
- reconstruct observed state;
- mark ordinary Instances UNLOADED;
- reconcile Always-On Instances;
- manual-stop suppression is cleared because it is session-local.

## 20. Resource eviction lifecycle

When Phase 7 scheduler execution chooses an Instance as an eviction victim:

1. revalidate that exact Instance is still eligible;
2. mark DRAINING;
3. remove it from routing;
4. allow active work to finish within policy;
5. if it is Always On, record `resource_pressure` as the temporary unsatisfied desired-state reason before stopping;
6. stop it;
7. release/refresh resources;
8. continue the requested Instance start.

Normal resource-pressure eligibility is controlled by `eviction_enabled`, not `always_on`.

An Instance with `eviction_enabled = false` is protected from normal resource-pressure eviction regardless of whether Always On is enabled. An Always-On Instance with `eviction_enabled = true` may be evicted, remains desired-running, and is reconciled non-preemptively once resources permit.

## 21. Lifecycle API semantics

Examples:

- Launch may return `QUEUED`/`STARTING` immediately.
- Stop may return `DRAINING`/`STOPPING`.
- repeated Launch while already starting is idempotent for that Instance;
- repeated Stop on UNLOADED is idempotently successful;
- Kill explicitly means immediate termination behavior;
- inference autoload waits internally because the client request must ultimately execute or fail.

## 22. Failure classifications

At minimum:

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

## 23. Invariants

1. Lifecycle belongs to Instances.
2. Only READY Instances receive new inference requests.
3. One startup operation per Instance is active at a time.
4. A request for one `instance.id` never silently uses a sibling Instance.
5. Always On reconciles the exact Instance whenever resources permit.
6. Always On does not itself protect an Instance from resource-pressure eviction.
7. `eviction_enabled=false` is the normal resource-pressure protection control.
8. Manual Stop can temporarily suppress Always-On reconciliation and stays distinct from resource-pressure blocking.
9. An evicted Always-On Instance never immediately preempts another Instance merely to satisfy its own desired state.
10. Idle unload never stops an active request.
11. PID/private port are cleared on process exit.
12. Unexpected exits never remain displayed as READY.
13. Direct Instance edits confirm and then automatically restart when needed.
14. Instance rename warns that the OpenAI model ID changes.
15. Manager restart never trusts stale READY state.

## 24. Acceptance criteria

Tests demonstrate:

- manual Launch from UNLOADED to READY;
- graceful Stop to UNLOADED;
- Kill terminates immediately;
- controlled Restart lifecycle;
- autoload for a stopped Instance;
- 20 simultaneous requests for one Instance produce one startup;
- requests for sibling Instances remain independent;
- Always-On Instance starts after manager boot;
- Always-On crash causes bounded retry;
- manual Stop keeps an Always-On Instance stopped during the current manager session;
- targeted inference can clear that suppression and start it;
- manager restart clears suppression and restores Always-On behavior;
- idle unload does not interrupt active work;
- direct running-Instance edit confirms and restarts automatically;
- Instance rename requires API-breaking warning;
- resource eviction drains the selected Instance;
- the four `always_on` × `eviction_enabled` combinations behave independently;
- an eviction-enabled Always-On Instance may be selected as an idle victim;
- an eviction-disabled Instance is protected whether or not it is Always On;
- an evicted Always-On Instance remains explicitly resource-pressure-blocked instead of being marked manually stopped;
- automatic Always-On reconciliation does not cause immediate eviction/restart oscillation;
- the resource-pressure block clears and the Always-On Instance starts when capacity returns;
- manager restart discards stale PID/READY state.
