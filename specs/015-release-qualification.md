# Release Qualification

## Purpose

Release qualification is the final 1.0 safety gate above normal unit, coverage, build and static-analysis CI. It validates the exact container build that will be published and exercises failure modes that are not adequately represented by isolated unit tests.

Stable release tags MUST NOT be published until the candidate image has completed the mandatory hosted and real-hardware qualification jobs.

## CI baseline

Every change continues to require the repository coverage gates. Backend CI additionally runs the Go race detector, `go vet` and `govulncheck`. Frontend and container builds install dependencies from committed lockfiles without resolving a new dependency graph.

## Candidate image contract

Release-container jobs build CPU and CUDA candidates locally first. The exact local images receive startup smoke qualification before any registry publication. Only successful builds are pushed under commit-specific `qualification-*` tags.

After upload, qualification tags are resolved to immutable `ghcr.io/brantje/llamarack@sha256:...` references. Hosted qualification, GPU qualification and final publication all consume those digest references. Stable tags MUST NOT be produced by a second rebuild or by resolving a mutable candidate tag after qualification.

Hosted container qualification verifies:

- `/health` responds successfully;
- the generated frontend is served;
- an empty data directory migrates successfully;
- first-user bootstrap and local login work;
- a persistent setting can be written and read;
- service-account and API-key creation works;
- the manager restarts against the same data directory;
- the bearer signing key, settings, service account and non-secret API-key identity remain usable after restart;
- CPU packaging boots without CUDA;
- CUDA packaging boots far enough to validate the manager image when no GPU is present.

## Deterministic software qualification

The reusable software suite first runs named acceptance groups, then runs the complete database, supervisor, downloads, models and lifecycle package integration suites. The named groups make release evidence directly traceable to issue #120 rather than requiring a reviewer to infer coverage from a broad package pass.

The acceptance groups cover:

- `upgrade-acceptance`: fresh Goose migration, latest-schema reopen/idempotency, baseline-to-next migration and failed-migration rollback/retry;
- `recovery-acceptance`: surviving owned-worker termination/replacement, unrelated-process protection, PID-reuse/generation mismatch safety and stale metadata cleanup;
- `download-acceptance`: Range resume, changed remote identity, failed partial download and deterministic disk-full recovery;
- `filesystem-acceptance`: artifact-path/symlink boundaries and safe model-file deletion planning;
- `lifecycle-acceptance`: active-request idle protection, idle unload, eviction policy, concurrent admission during drain, cancellation while waiting and missing-GGUF recovery.

The full package passes additionally remain authoritative deterministic coverage for rejection of unmanaged/foreign/newer database schemas, reservations, autoload, Always-On reconciliation, scheduler placement, routing and adjacent lifecycle branches.

Qualification scripts MUST compose these tests rather than duplicating their implementation logic in shell.

## Upgrade qualification

Before a previous release candidate exists, the migration gate exercises the exact embedded baseline plus a synthetic next migration, including rollback/retry and durable reopen. Once an RC database artifact exists, it becomes a retained release fixture and MUST be added to the same gate for RC-to-current qualification; an unavailable historical RC is not fabricated by reconstructing undocumented schema state.

Every retained release fixture must be opened by the current candidate, migrated once, reopened idempotently and checked for durable user/authentication, key/service-account, model, Instance, option and setting state that existed in that fixture.

## GPU runner

Real-hardware qualification runs on GitHub Actions runners carrying both labels:

```text
self-hosted
gpu-runner
```

The runner is expected to provide Docker with NVIDIA container GPU access, `nvidia-smi`, and at least one visible GPU. Stable release qualification on the project GPU runner is expected to expose two or more GPUs so multi-GPU placement is exercised; a single-GPU host still runs the single-device lifecycle, pressure/eviction, and MoE CPU-offload paths.

Two representative GGUFs must already exist on the runner host. Default paths are:

```text
/models/qualification.gguf
/models/qualification-moe.gguf
```

Repositories may override these with the Actions variables:

```text
GPU_QUALIFICATION_MODEL_PATH
GPU_QUALIFICATION_MOE_MODEL_PATH
```

Missing qualification models or zero visible GPUs are release-gate failures. The dense model must be large enough that a small number of high-demand workers pinned to one GPU reach resource pressure within the bounded eviction scenario; otherwise qualification fails with a provisioning error rather than pretending eviction was exercised. Multi-GPU placement is required when two or more GPUs are visible and must not be silently skipped on those hosts.

## Real-hardware soak

The CUDA candidate is run with all GPUs exposed. Qualification models are mounted beneath the manager's `/models` root so model registration uses the same filesystem boundary as production. The suite bootstraps a clean manager and exercises repeated combinations of:

- model registration and Instance creation;
- real non-streaming inference;
- streaming inference and explicit client cancellation;
- concurrent inference;
- concurrent starts with the single-worker invariant;
- inference-triggered autoload;
- per-Instance idle unload and subsequent autoload recovery;
- Always-On automatic start, Kill recovery and manual-Stop suppression;
- repeated stop and restart cycles;
- real single-GPU resource pressure and eviction;
- manager termination while inference is active;
- manager termination while a worker start is in progress;
- stale-worker reconciliation and replacement;
- multi-GPU placement;
- MoE CPU expert offload using `n-cpu-moe`;
- persistent settings and authentication across manager restart.

The process-only crash scenario deliberately keeps the container alive while SIGKILLing the manager so a managed `llama-server` child can survive long enough for the next manager process to prove ownership and reconcile it.

## Invariants

Qualification MUST fail if it observes any of the following:

- more than one managed worker for one logical Instance;
- a stale managed worker remaining after recovery;
- replacement worker identity reusing the stale PID;
- failed recovery preventing a later healthy inference;
- an active request being selected as an eviction victim in deterministic admission coverage;
- a pressure scenario that never exercises a real eviction;
- autoload, idle-unload or Always-On reconciliation failing to reach the expected lifecycle state;
- the MoE worker not receiving CPU-expert-offload flags;
- the multi-GPU MoE worker not receiving the expected comma-separated `--device` selection;
- unbounded manager goroutine growth;
- unbounded manager-process RSS growth.

The manager exports `llamarack_manager_goroutines` as a normal Prometheus gauge. The hardware soak samples that gauge and the manager process `VmRSS` throughout the scenario. Growth is evaluated from beginning/end sample windows rather than from the whole container, because worker/model memory is intentionally variable during lifecycle tests.

## Evidence

Qualification jobs upload artifacts even on failure where possible. Evidence includes:

- LlamaRack commit SHA;
- exact immutable image reference and image metadata;
- resolved Go toolchain for deterministic software qualification;
- scenario start/end timestamps;
- named acceptance-group logs;
- manager logs;
- per-scenario logs;
- GPU inventory and driver information;
- streamed and cancelled-inference samples;
- worker command/environment evidence for MoE placement;
- manager goroutine/RSS samples and growth summary.

Evidence must contain enough information to identify the exact candidate under test without exposing generated bearer tokens, API-key secrets or other credentials.

## Release gating

Main-branch container publication requires hosted software and container qualification. Stable GitHub releases additionally require the `gpu-runner` hardware job. Publication jobs depend on those qualification jobs and only retag the already-qualified candidate digests.

Performance benchmarking and exhaustive GPU-vendor coverage are not part of this gate. Multi-node qualification and persistent KV-cache qualification remain separate feature concerns.
