# 002 — Data Model

Status: Draft

Related issue: #1

## 1. Purpose

This document defines the durable domain model for llamacpp-manager. It is intentionally conceptual: exact SQL syntax and migration details belong to implementation, but entity boundaries, ownership and invariants are fixed here.

SQLite is the authoritative durable store for application configuration and state that must survive manager restarts.

## 2. Design principles

- Public model identity is independent from filenames and worker ports.
- Configured desired state is separate from observed runtime state.
- Runtime instances are first-class entities because one model may have multiple manually configured instances.
- llama.cpp options are stored as normalized key/value overrides rather than one database column per CLI flag.
- Secrets are stored separately from ordinary settings.
- High-frequency metrics are not modeled as permanent relational history by default.
- Download artifacts remain distinct from configured models so one artifact can exist before a model is created.
- V1 management users do not have differentiated roles or permissions.

## 3. Core entities

### 3.1 User

Represents a person allowed to use the management UI/API.

Fields:

- `id` — internal stable identifier;
- `username` — unique case-normalized login name;
- `password_hash`;
- `enabled`;
- `created_at`;
- `updated_at`;
- `last_login_at` optional.

Invariants:

- usernames are unique;
- disabled users cannot create new authenticated sessions;
- all authenticated users have the same full management access in v1;
- the system should protect against accidentally removing/disabling the last enabled management user unless a documented recovery flow exists.

RBAC fields such as `role`, capability grants or per-user scopes are not part of the v1 product contract. An implementation may retain dormant schema fields for forward compatibility, but they must not affect v1 behavior or be exposed as configurable v1 features.

### 3.2 Session

Represents a dashboard/management login session.

Fields:

- `id`;
- `user_id`;
- opaque session-token hash or equivalent server-side session identifier;
- `created_at`;
- `expires_at`;
- `last_seen_at`;
- optional metadata such as user agent or source IP if retained.

Sessions are revocable and are distinct from inference API keys.

### 3.3 API key

Represents a bearer credential for `/v1/*`.

Fields:

- `id`;
- `name`;
- `key_prefix` — safe display prefix;
- `key_hash`;
- `enabled`;
- `created_by_user_id`;
- `created_at`;
- `last_used_at` optional;
- `revoked_at` optional.

The plaintext key is returned only at creation/rotation time and never stored.

V1 keys are not restricted to selected models unless later requirements add model-level scopes.

### 3.4 Application setting

Stores durable global settings that are not secrets.

Examples:

- default idle timeout;
- default model priority;
- default routing policy;
- configured models directory;
- llama-server binary path if configurable;
- startup timeout defaults;
- global llama.cpp option defaults.

Settings should be validated through typed application logic even if stored in a flexible representation.

### 3.5 Secret

Stores encrypted provider/application secret material.

Initial uses:

- global Hugging Face token.

Fields conceptually include:

- `id`;
- `name/type`;
- encrypted ciphertext;
- encryption metadata/version;
- `created_at`;
- `updated_at`.

Secrets must never be included in generic settings API responses.

### 3.6 llama.cpp binary profile

Describes the active `llama-server` executable and the option schema discovered from it.

Fields:

- `id`;
- binary path or identifier;
- version/build information;
- fingerprint/hash where practical;
- accelerator/backend information when detectable;
- discovered-at timestamp;
- raw help fingerprint/hash;
- schema version.

Only one profile is active in the simple v1 deployment, but historical profiles may be retained if useful for diagnosing configuration changes.

### 3.7 llama.cpp option definition

Normalized metadata for one discovered command-line option.

Fields:

- `binary_profile_id`;
- canonical option name;
- aliases;
- description;
- inferred type;
- default if discovered;
- allowed values if discoverable;
- category;
- whether it is curated/basic;
- whether it is unknown/unclassified;
- repeatability/multi-value metadata where relevant.

The primary key must distinguish definitions belonging to different binary profiles.

### 3.8 Model artifact

Represents model files available locally or known through a provider/download operation.

Fields:

- `id`;
- logical display name;
- artifact type, initially GGUF;
- local completion state;
- local primary path;
- total bytes;
- checksum if known;
- provider type;
- provider repository/source identifier;
- provider revision optional;
- quantization metadata if known;
- architecture/parameter metadata if known;
- created/imported/downloaded timestamps.

An artifact may represent one GGUF file or one logical multi-shard GGUF set.

### 3.9 Artifact file

Represents physical files belonging to a logical model artifact.

Fields:

- `id`;
- `artifact_id`;
- relative path under the configured model directory;
- shard index optional;
- shard count optional;
- size;
- checksum optional;
- state.

For a single-file GGUF, one artifact has one artifact file. For split GGUF, one artifact has multiple ordered files.

Filesystem paths stored in the database must be canonicalized relative paths where possible. Provider-supplied filenames are never trusted as arbitrary paths.

### 3.10 Model

Represents the user-facing inference model.

Fields:

- `id` — internal identifier;
- `model_id` — unique public user-defined alias used in `/v1` requests;
- `display_name` optional;
- `artifact_id`;
- `enabled`;
- `autoload_enabled`;
- `always_on`;
- `idle_timeout_seconds` nullable/inherited;
- `startup_timeout_seconds` nullable/inherited;
- `priority` — `low`, `normal`, `high`;
- `routing_policy`;
- timestamps.

The public `model_id` must be unique among enabled/configured models and should be stable if the backing artifact is replaced.

A model references exactly one active artifact in v1.

### 3.11 Model configuration override

Stores llama.cpp settings that override global defaults for one model.

Fields:

- `model_id` internal FK;
- canonical option key;
- serialized normalized value;
- source/validation metadata optional;
- updated timestamp.

The effective configuration is:

```text
global defaults
      +
model overrides
      =
effective model configuration
```

There is no named preset tree in v1.

Options not present in the current binary schema must not be silently discarded; they should be retained but marked unsupported until the active binary exposes them again or the user removes them.

### 3.12 Instance definition

Represents a manually configured potential runtime instance for a model.

Fields:

- `id`;
- `model_id`;
- `name` optional/user-friendly;
- `enabled`;
- `preferred` flag or ordering if used by fixed routing;
- GPU assignment mode;
- selected GPU identifiers;
- tensor split configuration;
- optional instance-specific operational settings where explicitly supported;
- created/updated timestamps.

Important distinction: an instance definition is durable desired configuration; it is not proof that a process is currently running.

V1 should generally keep llama.cpp model options at model level. Instance-level fields should focus on placement/resource assignment unless a clear requirement demands per-instance inference differences.

### 3.13 Runtime instance state

Represents the currently observed worker lifecycle state for an instance definition.

Fields/data exposed by the service include:

- instance definition ID;
- lifecycle state;
- process ID;
- private port;
- started-at time;
- ready-at time;
- last-request-at time;
- active request count;
- queued request count if applicable;
- worker health status;
- last exit code;
- last failure summary;
- effective config fingerprint;
- observed GPU/resource allocation.

Not all of this needs to be durably persisted. In particular, PID, private port and READY status are ephemeral and must be reconstructed after restart.

If runtime state is persisted for diagnostics, it must be marked stale on manager startup until re-observed.

### 3.14 Download job

Represents one durable download operation.

Fields:

- `id`;
- provider;
- source/repository/url;
- requested file or logical artifact selection;
- target artifact ID optional until resolved;
- state;
- total bytes if known;
- completed bytes;
- retry count;
- last error;
- created/started/completed timestamps;
- cancellation marker where needed.

States are defined in the provider/download specification.

### 3.15 Provider cache metadata

Provider search results do not need full permanent relational persistence. Short-lived cached metadata may be stored with expiration if necessary to reduce API calls.

Provider metadata must never be treated as authoritative local artifact state until download validation completes.

## 4. Hardware identity model

The scheduler needs stable-enough device identifiers across polls.

A normalized GPU device contains:

- provider/vendor;
- stable hardware ID where available;
- index used by the active runtime backend;
- name;
- total memory;
- free/used memory snapshot;
- utilization snapshot;
- health/availability;
- backend-specific attributes where required.

Instance GPU assignments should reference stable hardware IDs plus any backend index required for launch. If device identity changes after reboot/driver change, the system must detect an unresolved assignment rather than silently binding to a different GPU.

High-frequency hardware samples are ephemeral unless a bounded recent-history feature is added.

## 5. Derived views

The application should expose derived domain views rather than force clients to reconstruct joins.

Examples:

### Model summary

- public model ID;
- display name;
- artifact/quantization;
- policy flags;
- effective priority;
- aggregate lifecycle status;
- ready/defined instance counts;
- active requests;
- last used;
- estimated resource requirements.

### Effective configuration

- global value;
- model override if present;
- resulting effective value;
- support/validation state against the active llama.cpp profile.

### Instance summary

- definition;
- desired enabled state;
- observed state;
- PID/private port as secondary diagnostics;
- GPU allocation;
- request counts;
- current health;
- recent failure.

## 6. Model ID rules

Public model IDs are user-defined aliases and are part of the external API contract.

Rules:

- must be unique;
- must be non-empty;
- should use a conservative URL/JSON-safe character set;
- must not include path traversal semantics;
- comparison behavior must be deterministic; v1 should use exact case-sensitive identity unless product UX chooses enforced lowercase;
- renaming a model ID is an explicit API-breaking action for clients and should be presented as such in the UI.

GGUF filenames and Hugging Face repository names do not automatically become public IDs unless the user accepts a generated suggestion.

## 7. Deletion semantics

Deleting a model definition does not automatically have to delete the artifact file.

Recommended separation:

- **Delete model** — removes the configuration/public ID after stopping instances.
- **Delete artifact** — removes local files only when no model references them, unless an explicit destructive workflow also removes dependents.

This avoids accidental multi-gigabyte redownloads when recreating model configuration.

API keys and users should prefer revocation/disable semantics where useful for auditability rather than immediate irreversible erasure.

## 8. Configuration versioning and fingerprints

Each model should have a deterministic effective configuration fingerprint derived from:

- backing artifact identity;
- effective llama.cpp options;
- relevant instance placement settings;
- active llama.cpp binary profile.

A running instance records the fingerprint it was launched with.

If desired fingerprint differs from observed fingerprint, the model/instance is `restart_required` even if the process remains healthy.

This is preferable to trying to compare individual fields at runtime.

## 9. Persistence and transaction requirements

Operations that combine durable state changes must be transactional where practical.

Examples:

- creating a model plus its first instance definition;
- rotating an API key metadata record;
- marking a completed download plus final artifact files;
- updating model configuration version/fingerprint inputs.

Process startup itself cannot be made part of a SQLite transaction. Use explicit state transitions and reconciliation instead of holding database transactions across child-process operations.

## 10. Data not stored by default

V1 should not persist:

- prompts;
- generated completion content;
- full inference request/response bodies;
- arbitrary client headers;
- indefinite per-request traces;
- indefinite high-frequency GPU telemetry.

This reduces privacy risk and uncontrolled database growth.

Request metrics should be aggregate counters/histograms unless a bounded diagnostic feature is explicitly added.

## 11. Migration policy

All durable schema changes use ordered migrations.

Requirements:

- migrations are applied automatically at startup before normal service begins;
- each migration is idempotently tracked;
- downgrade support is not required for every migration, but backups and release notes should warn when a schema change is irreversible;
- migrations must not assume runtime worker processes are active;
- schema version must be observable in diagnostics.

## 12. Invariants

1. A Model always points to a completed usable artifact.
2. A public `model_id` uniquely identifies one configured Model.
3. A Runtime Instance can only refer to one durable Instance Definition.
4. PID/private port values never prove liveness after manager restart.
5. Plaintext passwords, API keys and provider tokens are never persisted.
6. A completed artifact never references `.part` or otherwise incomplete files.
7. Model option overrides survive temporary incompatibility with a newer/older llama.cpp binary unless the user removes them.
8. Instance GPU assignments cannot silently retarget an unknown different device.
9. Model deletion and artifact deletion are separate operations.
10. Metrics/history retention must remain bounded.
11. V1 user records do not require RBAC semantics.

## 13. Acceptance criteria

The data model is adequate when it can represent:

- two public model IDs backed by two different local GGUF artifacts;
- one model with two manually configured instances on different GPUs;
- an unloaded model whose desired configuration persists across restart;
- a running instance whose ephemeral PID/port are discarded and reconstructed after manager restart;
- global llama.cpp defaults plus per-model overrides;
- an override that becomes temporarily unsupported after a binary change without losing the stored value;
- a split GGUF represented as one logical artifact with multiple files;
- a resumable Hugging Face/direct URL download;
- a disabled API key without retaining its plaintext secret;
- an encrypted global Hugging Face credential;
- one or more equivalent local management users without role-specific behavior;
- Always-On and idle-timeout lifecycle policies without encoding them into worker runtime state.