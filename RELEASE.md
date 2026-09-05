# Release and compatibility policy

This document defines the stable `1.x` support contract and the release process used for official LlamaRack container images.

## Semantic versioning and the `1.x` contract

LlamaRack follows Semantic Versioning for documented stable behavior.

- `1.0.x` releases contain compatible fixes and security updates.
- `1.x` minor releases may add features while remaining backward compatible with the documented `1.x` contract.
- `2.0` is the next normal place for intentional breaking changes to that contract.

### Stable in `1.x`

The following are part of the documented stable contract unless a specific surface is explicitly marked experimental:

- documented OpenAI-compatible `/v1` behavior;
- documented management `/api/v1` behavior;
- durable Instance IDs used as OpenAI `model` IDs;
- the database forward-upgrade path;
- documented environment and configuration keys;
- the documented container and Compose deployment contract.

Compatible additions are allowed. Existing documented behavior should not be removed, renamed, or changed incompatibly in a `1.x` release without a deprecation/migration path where one is practical.

### May evolve compatibly in `1.x`

The following implementation details may change as long as the stable contract remains usable:

- UI implementation and visual details;
- recommendation heuristics and estimated fit/speed guidance;
- the dynamically discovered llama.cpp option inventory;
- additive APIs, fields, metrics, and features;
- internal scheduling and lifecycle implementation details that preserve documented behavior.

A behavior being visible in source code does not by itself make it a stable public contract. Experimental surfaces must be identified as such in their documentation.

## Build identity

Every official release image exposes its build identity through authenticated `GET /api/v1/system` and **Administration → System**. The identity includes:

- LlamaRack semantic version;
- exact LlamaRack Git commit;
- deterministic source/build timestamp used by the release workflow;
- release/development channel;
- CPU/CUDA runtime variant;
- bundled llama.cpp release;
- immutable llama.cpp `bNNNN` build identifier;
- exact upstream runtime image reference selected by the workflow.

The same values are added to OCI image labels. A release-channel image must not identify itself only as `development`.

For support requests, the Administration → System identity is preferred over guessing from a floating container tag.

## Reproducible release inputs

Official builds treat dependency metadata as source input:

- the frontend is installed with `npm ci` from the committed `package-lock.json`;
- Go dependencies come from committed `go.mod` and `go.sum` and the container build runs `go mod download` / `go mod verify`, not `go mod tidy`;
- the build uses `-trimpath` and injects release identity explicitly;
- release build time is derived deterministically from the release commit timestamp rather than wall-clock build time;
- the selected llama.cpp runtime is pinned to an immutable upstream build identifier for that release candidate;
- workflow actions and builder images used in the release path must use immutable references (commit SHAs or image digests) rather than mutable tags or implicitly resolved versions.

Rebuilding a release from the same source inputs must not silently choose a different llama.cpp runtime.

## Release candidates and llama.cpp selection

Every `1.0.0-rc.N` (and later release candidate following this policy) performs a fresh upstream lookup when that candidate is cut:

1. pin the current published `ghcr.io/ggml-org/llama.cpp` `server` and supported `server-cuda*` images to immutable digests;
2. read the CPU/CUDA images' `org.opencontainers.image.version` and require the same `bNNNN` build;
3. query `ggml-org/llama.cpp` for the latest normal/stable GitHub release (reject drafts and prereleases);
4. if that GitHub release's immutable `bNNNN` matches the published GHCR images, record the GitHub release name;
5. otherwise record the published GHCR `bNNNN` as the bundled runtime identity (upstream container images are published on a different cadence than GitHub stable releases, so `server-bNNNN` tags for `releases/latest` may never exist);
6. build LlamaRack CPU/CUDA candidates with those exact digest references;
7. verify `/api/v1/system` reports the expected LlamaRack and llama.cpp identity;
8. run release qualification against the exact candidate image digests;
9. only after qualification succeeds, publish the RC semantic tags.

There is no fallback to a different GitHub stable llama.cpp release by name. If the current GHCR CPU/CUDA server images cannot be resolved, report different `bNNNN` builds, or are incompatible with LlamaRack, the LlamaRack release candidate is blocked until compatibility is restored.

A later RC repeats the lookup. Therefore RC2 may intentionally bundle a newer stable llama.cpp release than RC1 if upstream published one in between.

## Stable promotion

The final stable release does **not** perform another "latest llama.cpp" lookup.

For a stable release such as `1.0.0`, the release workflow finds the highest published `1.0.0-rc.N`, then verifies that its CPU and CUDA images:

- were built from the exact Git commit tagged by `1.0.0`;
- identify CPU and CUDA variants correctly;
- identify the same llama.cpp release and `bNNNN` build;
- record the exact runtime images used during RC qualification.

The stable images are rebuilt from that same LlamaRack commit only to replace the prerelease product version with the stable SemVer identity. They reuse the final RC's recorded llama.cpp runtime references unchanged and are smoke-tested before publication. A newer llama.cpp release that appeared after the final RC is deferred to the next LlamaRack release.

The GitHub Release receives a `build-identity.json` asset and an automatically appended bundled-runtime section containing the selected runtime and published image digests.

## Container tag policy

Official CPU tags use:

```text
1.0.0-rc.1
1.0.0
1.0
1
latest
```

CUDA variants append `-cuda`:

```text
1.0.0-rc.1-cuda
1.0.0-cuda
1.0-cuda
1-cuda
latest-cuda
```

Rules:

- an exact semantic-version tag such as `1.0.0-rc.1` or `1.0.0` is immutable after first publication;
- a release workflow fails instead of moving an existing exact semantic tag;
- `1.0`, `1`, and `latest` are moving aliases and may advance only to a newer compatible stable release;
- `latest` never points to a prerelease;
- `main`, `main-<sha>`, `llama.cpp-latest`, and `llama.cpp-*` are development/update channels and are not stable semantic releases;
- the corresponding CUDA tag represents the same LlamaRack source and selected upstream llama.cpp release/build as the CPU tag.

Official release records must include CPU and CUDA image digests, runtime identity, and qualification results. Publication fails if any of these provenance fields is unavailable. Operators that require byte-for-byte deployment identity should deploy by digest.

## Database upgrades and backups

The database forward-upgrade path is part of the `1.x` contract. Schema migrations are applied automatically during startup. LlamaRack must not silently wipe a database because it is older or newer than expected.

Before upgrading across releases:

1. stop the manager;
2. back up `manager.db` and any `manager.db-wal` / `manager.db-shm` sidecars from the configuration volume;
3. retain that backup until the upgraded deployment has been verified;
4. restore the files before starting the older deployment if rollback is required and the newer release has already migrated the database.

Release notes must call out migrations, backup requirements, or rollback limitations that are material to that release.

## Release notes

A release note is an operator-facing compatibility record, not a commit dump. For each RC/stable release, include at least:

- important supported product changes and fixes;
- upgrade/migration/backup notes;
- compatibility or deprecation notes;
- bundled llama.cpp release and `bNNNN` build;
- CPU/CUDA image digests, runtime identity, and qualification results;
- known release blockers or intentionally deferred work.

The durable project history lives in `CHANGELOG.md`; GitHub Release notes may summarize the same information more narratively.

## Release sequence

The expected stable release sequence is:

```text
freeze main for RC scope
→ resolve latest stable llama.cpp
→ resolve/pin immutable bNNNN runtime
→ build versioned CPU/CUDA candidates
→ verify build identity
→ run release qualification
→ publish 1.0.0-rc.N prerelease + images
→ bug fixes only
→ repeat latest-stable lookup + qualification for each later RC
→ tag final 1.0.0 at the final RC commit
→ verify final RC identity
→ rebuild stable version identity with the same runtime
→ smoke-test
→ publish 1.0.0 / 1.0 / 1 / latest and CUDA aliases
```

A stable release is blocked when these invariants cannot be proven.
