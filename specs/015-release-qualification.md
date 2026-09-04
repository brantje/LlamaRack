# Release Qualification

## Purpose

Release qualification is the final 1.0 safety gate above normal unit, coverage, build and static-analysis CI. It validates the exact container build that will be published and exercises failure modes that are not adequately represented by isolated unit tests.

Stable release tags MUST NOT be published until the candidate image has completed the mandatory hosted and real-hardware qualification jobs.

## CI baseline

Every change continues to require the repository coverage gates. Backend CI additionally runs the Go race detector, `go vet` and `govulncheck`. Frontend and container builds install dependencies from committed lockfiles without resolving a new dependency graph.

## Candidate image contract

Release-container jobs build CPU and CUDA candidates locally first. The exact local images receive startup smoke qualification before any registry publication. Only successful builds are pushed under commit-specific `qualification-*` tags.

Later release jobs pull those candidate images and retag the already-qualified image digest. Stable tags MUST NOT be produced by a second independent rebuild after qualification.

Hosted container qualification verifies:

- `/health` responds successfully;
- the generated frontend is served;
- an empty data directory migrates successfully;
- first-user bootstrap and local login work;
- a persistent setting can be written and read;
- service-account and API-key creation works;
- the manager restarts against the same data directory;
- the bearer signing key, settings and service account remain usable after restart;
- CPU packaging boots without CUDA;
- CUDA packaging boots far enough to validate the manager image when no GPU is present.

## Deterministic software qualification

The reusable software suite runs the database, supervisor, downloads, models and lifecycle package integration tests as one release scenario. Those tests are the authoritative deterministic coverage for:

- Goose migration execution, idempotency, upgrade and rollback;
- rejection of unmanaged/foreign/newer database schemas;
- stale owned-worker reconciliation and unrelated-process safety;
- interrupted and resumed downloads, changed remote identity and partial-file handling;
- disk-full/write failure without promoting an incomplete GGUF;
- model artifact deletion boundaries and missing-GGUF recovery;
- lifecycle, admission, eviction, reservation, autoload, Always-On and idle-unload invariants.

Qualification scripts MUST compose these tests rather than duplicating their implementation logic in shell.

## GPU runner

Real-hardware qualification runs on GitHub Actions runners carrying both labels:

```text
self-hosted
gpu-runner
```

The runner is expected to provide Docker with NVIDIA container GPU access, `nvidia-smi`, and at least two visible GPUs for stable release qualification.

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

Missing qualification models or fewer than two visible GPUs are release-gate failures. Stable qualification MUST NOT silently downgrade to a reduced hardware matrix.

## Real-hardware soak

The CUDA candidate is run with all GPUs exposed. The suite bootstraps a clean manager and exercises repeated combinations of:

- model registration and Instance creation;
- real non-streaming inference;
- streaming inference;
- concurrent inference;
- stop and restart cycles;
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
- the MoE worker not receiving CPU-expert-offload flags;
- the multi-GPU worker not receiving visibility for multiple GPUs;
- unbounded manager goroutine growth;
- unbounded manager-process RSS growth.

The manager exports `llamarack_manager_goroutines` as a normal Prometheus gauge. The hardware soak samples that gauge and the manager process `VmRSS` throughout the scenario. Growth is evaluated from beginning/end sample windows rather than from the whole container, because worker/model memory is intentionally variable during lifecycle tests.

## Evidence

Qualification jobs upload artifacts even on failure where possible. Evidence includes:

- LlamaRack commit SHA;
- exact image reference and image metadata;
- scenario start/end timestamps;
- manager logs;
- per-scenario logs;
- GPU inventory and driver information;
- streamed inference samples;
- worker command/environment evidence for MoE placement;
- manager goroutine/RSS samples and growth summary.

Evidence must contain enough information to identify the exact candidate under test without exposing generated bearer tokens, API-key secrets or other credentials.

## Release gating

Main-branch container publication requires hosted software and container qualification. Stable GitHub releases additionally require the `gpu-runner` hardware job. Publication jobs depend on those qualification jobs and only retag the already-qualified candidate images.

Performance benchmarking and exhaustive GPU-vendor coverage are not part of this gate. Multi-node qualification and persistent KV-cache qualification remain separate feature concerns.
