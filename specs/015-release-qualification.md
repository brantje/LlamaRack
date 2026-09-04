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

Four representative GGUFs must already exist on the runner host under one models directory. Default directory:

```text
/home/node/github-runners/models
```

Required role files (symlinks allowed):

```text
qualification-llama-8B.gguf      # dense lifecycle + single-GPU pressure (or qualification.gguf)
qualification-12B.gguf           # multi-GPU dense placement (≥2 GPUs)
qualification-moe-4ba1b.gguf     # MoE CPU expert offload (or qualification-moe.gguf)
qualification-moe-26b-a4b.gguf   # multi-GPU MoE placement
```

Repositories may override the directory with the Actions variable:

```text
GPU_QUALIFICATION_MODELS_DIR
```

Individual absolute path overrides are also supported by the soak harness via `GPU_QUALIFICATION_DENSE_LIFECYCLE`, `GPU_QUALIFICATION_DENSE_MULTI`, `GPU_QUALIFICATION_MOE_SMALL`, and `GPU_QUALIFICATION_MOE_LARGE`. Overrides outside the models directory are bind-mounted into the candidate container.

Missing qualification models or zero visible GPUs are release-gate failures. Stable qualification MUST NOT silently downgrade to a reduced hardware matrix: when two or more GPUs are visible, multi-GPU placement must be exercised for both the 12B dense GGUF and the large MoE GGUF, and must not be skipped. The 8B dense pressure path must raise context until a small number of workers pinned to one GPU reach resource pressure within the bounded eviction scenario; otherwise qualification fails with a provisioning error rather than pretending eviction was exercised. The 12B GGUF is not a single-GPU model on 16 GB-class cards; a one-GPU host still requires the file on disk but skips the 12B start.

## Real-hardware soak

The CUDA candidate is run with all GPUs exposed. The four qualification GGUFs are mounted from one host models directory beneath the manager's `/models` root so model registration uses the same filesystem boundary as production. The suite bootstraps a clean manager, registers all four GGUFs, smoke-loads the models that fit the visible GPU count, then exercises repeated combinations of:

- model registration and Instance creation;
- real non-streaming inference;
- streaming inference and explicit client cancellation;
- concurrent inference;
- concurrent starts with the single-worker invariant;
- inference-triggered autoload;
- per-Instance idle unload and subsequent autoload recovery;
- Always-On automatic start, Kill recovery and manual-Stop suppression;
- repeated stop and restart cycles;
- real single-GPU resource pressure, active-request protection, and eviction (8B dense GGUF);
- manager termination while inference is active;
- manager termination while a worker start is in progress;
- stale-worker reconciliation and replacement;
- multi-GPU dense placement (12B GGUF when two or more GPUs are visible);
- multi-GPU MoE placement (large MoE GGUF when two or more GPUs are visible);
- MoE CPU expert offload using `n-cpu-moe` (small and large MoE GGUFs);
- persistent settings and authentication across manager restart.

Dense lifecycle and single-GPU pressure/eviction use the 8B dense GGUF. The 12B dense GGUF is reserved for multi-GPU placement: it is started with both visible devices so the comma-separated `--device` and generated `--tensor-split` launch path is proven. It is not pinned to one 16 GB-class card.

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
