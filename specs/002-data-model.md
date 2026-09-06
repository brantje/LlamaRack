# 002 — Data Model

Status: Draft

Related issue: #1

## 1. Purpose

This document defines the durable domain model for LlamaRack.

The core separation is:

- **Model** = registered management-plane model configuration;
- **Instance** = durable configured `llama-server` process definition and inference identity;
- **Runtime state** = ephemeral observed process state for an Instance.

SQLite is the durable store for configuration that must survive manager restarts.

## 2. Release schema policy

LlamaRack uses embedded Goose migrations for SQLite schema evolution. See [014 — Database Migrations](014-database-migrations.md).

For active development on unreleased schema:

- add a new immutable Goose migration for every schema change after the 1.0 baseline;
- update fixtures/seeds/tests in the same change;
- do not add runtime `ensure*Schema`, `PRAGMA table_info`, or guarded `ALTER TABLE` compatibility helpers to application code.

Before `1.0.0`, incompatible databases must be recreated or restored from a Goose-managed backup. There is no pre-Goose upgrade path.

## 3. Design principles

- Models and Instances are distinct first-class entities.
- A Model can exist without an Instance.
- Multiple Instances may reference one Model.
- Lifecycle/scheduler state belongs to Instances, not Models.
- Desired Instance configuration is separate from observed runtime state.
- llama.cpp configuration uses inheritance rather than duplicated full configs.
- Durable resource identity is separate from mutable human/public identity.
- High-frequency metrics are not permanently relational by default.
- Secrets remain separate from normal settings.
- Raw GGUF files remain the source of truth for embedded model metadata.
- Arbitrary GGUF metadata must not require one relational column per metadata key.

## 4. Resource identity convention

Long-lived first-class resources use three distinct concepts when they need human-addressable routes:

- `id` — immutable durable machine identity and foreign-key target;
- `slug` — unique mutable human/public route identity;
- `name` — mutable display label.

This convention applies to Models and Instances. It should also be used by future first-class resources such as Nodes when they require mutable human-friendly routes. It is not a requirement to add slugs to event rows, metrics, request IDs, jobs, or other machine-only records.

Names and slugs are independent after creation. A name change does not implicitly change a slug. A slug change is an explicit operation with collision validation and an impact warning appropriate to the resource.

### 4.1 User / Session / Service account / API Key / Secret

These retain the existing v1 security model with typed owner-bound keys:

- local management users;
- server-side sessions;
- service accounts (name, enabled, created_at, optional created_by_user_id);
- hashed API keys (`sk-` secrets) with:
  - `key_type` of `inference`, `management`, or `full`;
  - exactly one owner (`owner_user_id` or `owner_service_account_id`);
  - optional `expires_on` (`YYYY-MM-DD`, valid through end of that UTC day);
  - optional inference `instance_ids` allowlist containing immutable Instance IDs;
  - `enabled`, `prefix`, `last_used_at`;
  - no `revoked_at`; rotate replaces `token_hash` and `prefix` in place;
- deleting a user or service account cascades and deletes that owner's keys;
- encrypted provider secrets;
- no management RBAC in v1.

### 4.2 llama.cpp binary profile and option definitions

Store the active `llama-server` identity/fingerprint and discovered option schema.

Option definitions include canonical key, aliases, description, inferred type, defaults/allowed values where discoverable, category and Basic/Advanced metadata.

### 4.3 Model artifact

Represents one logical local model artifact.

Fields include:

- `id`;
- logical display name;
- artifact type, initially GGUF;
- local primary path;
- total bytes;
- checksum if known;
- provider/source metadata;
- quantization;
- architecture/parameter/context summary metadata where known and useful;
- completion state;
- timestamps.

Split GGUFs remain one logical artifact with multiple Artifact File rows.

GGUF metadata inspection adds a shared GGUF inspection/cache associated with the logical artifact. The raw GGUF remains authoritative; the cache is only a performance layer for metadata inspection and derived product values.

The cache should conceptually include:

- inspector schema/version;
- artifact/shard fingerprint used to detect staleness;
- GGUF format/version;
- raw GGUF metadata entries preserving key, value type and value representation;
- inspection warnings/status;
- inspected timestamp.

Do **not** create a relational column for every arbitrary GGUF metadata key. Frequently queried product fields such as Context capability or other values required by recommendation logic may be stored separately when useful, but the generic metadata view must not depend on a hard-coded database schema for each GGUF key.

Large metadata arrays may use a bounded/lazy representation as long as every metadata key remains inspectable.

### 4.4 Model

A Model is a registered/configured management-plane model.

Conceptual fields:

- `id` — immutable stable Model identifier and foreign-key target;
- `slug` — unique mutable management/UI route identifier;
- `name` — user-facing Model name;
- `artifact_id`;
- `enabled` if needed for management availability;
- timestamps.

Model slugs have no OpenAI inference meaning. Changing a Model slug changes management URLs/bookmarks only; it does not change any Instance slug or OpenAI `model` value.

Model-derived summary data may expose:

- backing path;
- size;
- quantization;
- context capability;
- basic GGUF inspection status.

A Model does **not** contain runtime/lifecycle policy such as:

- `autoload_enabled`;
- `always_on`;
- `eviction_enabled`;
- runtime priority;
- GPU assignment;
- READY/UNLOADED state.

A Model references exactly one active artifact in v1.

### 4.5 Model llama.cpp override

Stores reusable llama.cpp overrides for one Model.

Fields:

- immutable Model ID FK;
- canonical option key;
- normalized serialized value;
- validation/source metadata where useful;
- updated timestamp.

These values are defaults inherited by Instances.

### 4.6 Instance

An Instance is one durable configured potential `llama-server` process.

Conceptual fields:

- `id` — immutable UUID and durable foreign-key target;
- `slug` — unique mutable public identifier and exact OpenAI `model` value;
- `name` — human-entered Instance display name;
- `model_id` — immutable Model ID FK;
- `enabled` where needed;
- `always_on`;
- `autoload_enabled`;
- `eviction_enabled`;
- `priority` — `low`, `normal`, `high`;
- `idle_timeout_seconds` nullable/inherited where supported;
- `max_pending_requests` — `0` inherits the manager `max_pending_requests_per_instance` default; a positive value is this Instance’s pending-request cap;
- `startup_timeout_seconds` nullable/inherited where supported;
- GPU assignment mode;
- selected GPU stable identifiers;
- tensor split mode/configuration;
- timestamps.

The Instance row is durable even while no process exists.

### 4.7 Instance identity rules

Instance creation defaults the slug from the name, but durable and public identity are separate:

```text
Instance name
   -> slugify on create
   -> instance.slug
   -> OpenAI model identifier

instance.id
   -> immutable UUID
   -> runtime ownership / foreign keys / allowlists / history correlation
```

Example:

```text
name = "Qwen Coding 32B"
slug = "qwen-coding-32b"
id   = "550e8400-e29b-41d4-a716-446655440000"
```

Clients use:

```json
{"model":"qwen-coding-32b"}
```

Rules:

- `instance.id` is immutable and never derived from later name/slug edits;
- `instance.slug` is globally unique among addressable Instances;
- default slug generation is deterministic and uses a conservative URL/JSON-safe character set;
- `instance.slug` is the exact OpenAI-compatible model identifier;
- renaming an Instance changes only `name` unless the user explicitly edits `slug`;
- changing `slug` is API-breaking for clients using the old OpenAI `model` value and requires explicit warning/confirmation;
- changing `slug` does not rewrite durable UUID references;
- duplicate/colliding slugs fail validation rather than silently target the wrong Instance;
- no hidden compatibility alias for an old slug is retained unless explicitly added as a future feature.

### 4.8 Instance llama.cpp override

Stores per-Instance llama.cpp values that override Model defaults.

Effective configuration:

```text
Global defaults
      +
Model overrides
      +
Instance overrides
      =
Effective Instance configuration
```

An absent Instance override means inherit from the Model/global layers.

Options temporarily absent from the active llama.cpp schema must be retained and marked unsupported rather than deleted.

### 4.9 Runtime Instance state

Observed runtime state is separate from durable Instance configuration.

It can expose:

- `instance_id` — immutable Instance UUID;
- lifecycle state;
- PID;
- private port;
- started/ready timestamps;
- last-request/last-activity time;
- active/queued requests;
- worker health;
- exit code/failure summary;
- effective launch fingerprint;
- observed GPU/resource allocation.

PID, port and READY state are ephemeral and cannot be trusted after manager restart.

A separate `worker_runtime` table stores only enough identity to reconcile after an abnormal manager restart:

- `instance_id` (plain immutable Instance ID text, no foreign key, so a deleted Instance can still be cleaned up);
- worker generation token;
- PID;
- process start identity (`start_ticks`);
- private port.

This table is not desired Instance configuration and must never override durable Instance rows. A durable `installation_id` in `manager_settings` (internal, not an admin General setting) labels workers belonging to this installation. Workers also receive `LLAMARACK_INSTALLATION_ID`, `LLAMARACK_INSTANCE_ID`, and `LLAMARACK_WORKER_GENERATION` in their environment so startup cleanup can prove ownership before terminating a process.

The managed `llama-server --alias` value is the current Instance slug. Process ownership, runtime maps, reservations and cleanup remain keyed by immutable Instance ID.

### 4.10 Download job / provider cache

Retain the existing durable download-job model and bounded provider cache behavior.

### 4.11 Inference request OpenAI Response state

`inference_requests` remains the single persistence source for inference traffic. Request history stores both:

- `instance_id` — durable Instance identity used for correlation/authorization/filtering;
- `model_slug` — exact OpenAI model slug captured for that request and never rewritten after a later rename.

OpenAI stored-Response support adds:

- `openai_response_id` (nullable text)
- `openai_response_deleted` (integer, default 0)

A partial unique index on non-null `openai_response_id` values enforces one Manager row per upstream Response ID. OpenAI deletion only sets `openai_response_deleted`; it does not delete the row or clear debugging bodies.

## 5. Configuration fingerprints

Each Instance has a deterministic desired launch fingerprint based on:

- active llama.cpp binary profile;
- backing Model artifact identity;
- effective Global + Model + Instance llama.cpp options;
- Instance placement/tensor split;
- manager-owned launch behavior affecting semantics.

A running worker stores the fingerprint it actually launched with.

Direct Instance edits that change the desired fingerprint trigger the controlled-restart workflow after user confirmation.

Changes to inherited Model/global defaults may mark affected running Instances as needing restart until the relevant UI flow applies them.

The GGUF metadata-cache fingerprint is separate from the Instance launch fingerprint. It only determines whether cached metadata still describes the current local artifact/shard set.

## 6. Derived views

### Model summary

For `/models`:

- Model name and slug;
- path;
- size;
- quantization;
- context capability;
- Details/Edit/Delete affordances.

Do not mix Instance runtime state into the Models table.

### Model details

For `/models/:slug/details`:

- a compact Model/GGUF summary;
- GGUF version/count/status where available;
- searchable access to the GGUF metadata entries as generic `key / type / value` data;
- all metadata keys, including manager-unknown keys;
- bounded/lazy expansion for large metadata values.

This is intentionally generic. The data model does not require separate architecture/tokenizer/MoE/etc. detail structures merely to render this page.

Instance lifecycle/runtime state does not move to Model details.

### Instance summary

For `/instances`:

- Instance `slug` and name;
- referenced Model;
- configured lifecycle/resource policy;
- observed runtime state;
- placement summary;
- health/failure information;
- runtime metrics as observability adds them.

The immutable UUID may be exposed in diagnostics/details where machine identity matters, but normal navigation uses the slug.

## 7. Model creation with optional first Instance

Creating a Model may optionally bootstrap one Instance.

The Model creation UI may collect only these Instance-specific settings:

- Instance name;
- Instance slug (defaulted from name);
- Always On;
- Autoload on request;
- Allow resource-pressure eviction;
- whether to start immediately.

Before Model save, GGUF inspection may inspect the selected local GGUF and pre-fill Model fields such as Context capability. Detected user-facing values remain editable. Failure to inspect metadata does not by itself block Model creation and must not erase an explicitly entered Context capability.

The backend must:

1. validate/inspect the selected logical GGUF artifact where possible;
2. create the Model with an immutable ID and unique slug;
3. persist accepted Model metadata/cache information where configured;
4. default the optional first Instance slug from its name when not explicitly provided;
5. validate slug uniqueness;
6. create the Instance with an immutable UUID;
7. apply the selected policies;
8. optionally request launch.

If process startup fails, do not delete the successfully created Model or Instance.

## 8. Deletion semantics

- **Delete Instance** — stop/kill as required, then remove the durable Instance definition.
- **Delete Model** — only allowed when dependent Instances are handled explicitly; do not accidentally orphan them.
- **Delete artifact** — separate destructive operation with dependency checks.

Deleting a Model does not implicitly delete a multi-gigabyte artifact unless the user explicitly performs that action.

Artifact metadata cache follows the artifact's lifecycle and must not retain stale orphaned cache records after artifact deletion.

## 9. Persistence and transactions

Durable multi-row operations should be transactional where practical.

Examples:

- creating a Model plus its optional first Instance definition;
- persisting Model metadata/cache information alongside successful Model registration;
- duplicating an Instance and its override rows;
- changing a slug while preserving all ID-based foreign-key references;
- marking download completion and artifact files.

Worker process startup cannot be inside a SQLite transaction. Persist desired state first, then execute lifecycle actions.

GGUF inspection/file I/O should not hold a long SQLite write transaction. Inspect/validate first, then transactionally persist the accepted result.

## 10. Rename and slug-change semantics

Names and slugs are independent after creation.

Required behavior:

- changing `name` alone preserves `id` and `slug`;
- changing `slug` preserves immutable `id` and every durable ID reference;
- validate slug uniqueness before save;
- Instance slug changes warn that clients using the old OpenAI `model` value will break;
- Model slug changes warn that management URLs/bookmarks change, but do not imply an inference/API break;
- do not retain hidden old-slug compatibility aliases unless explicitly added as a future feature.

## 11. Hardware identity

GPU assignments reference stable hardware IDs plus backend indices when required.

If a configured device disappears/reorders, mark the placement unresolved. Never silently bind to a different GPU.

## 12. Data not stored by default

V1 does not persist by default:

- prompts;
- generated completion bodies;
- arbitrary request headers;
- indefinite request traces;
- indefinite high-frequency GPU telemetry.

GGUF metadata cache, when used, contains artifact metadata only and must never contain tensor payload data.

## 13. Invariants

1. A Model references a usable completed artifact.
2. A Model may have zero or many Instances.
3. An Instance references exactly one Model by immutable Model ID.
4. Lifecycle/scheduler policy belongs to the Instance.
5. Model and Instance `id` values are immutable durable identities.
6. Model and Instance `slug` values are unique mutable human/public route identities.
7. `instance.slug` is the exact OpenAI `model` identifier; `model.slug` has no OpenAI meaning.
8. Name changes do not implicitly change slugs after creation.
9. Durable references, runtime ownership and API-key Instance scopes use immutable IDs, not slugs.
10. Request history preserves both durable Instance ID and the exact model slug captured for the request.
11. Runtime PID/port do not prove liveness after restart; stale owned workers are identified from installation/generation metadata plus start identity, then terminated before replacements launch.
12. Model and Instance llama.cpp overrides retain inheritance semantics.
13. Instance GPU assignments cannot silently retarget another device.
14. Model deletion and artifact deletion are separate.
15. Schema changes after the 1.0 baseline require a new immutable Goose migration in the same change, with fixtures/seeds/tests updated together; runtime schema compatibility helpers are prohibited.
16. The GGUF file/shard set is the source of truth; cached inspection must be invalidated/refreshed when its artifact fingerprint changes.
17. Arbitrary GGUF metadata keys are not modeled as one schema column each.
18. Model metadata/details never acquire Instance runtime lifecycle ownership.

## 14. Acceptance criteria

The data model is adequate when it can represent:

- a registered Model with no Instance;
- one Model with two independently configured Instances;
- Instance names `Coding` and `Coding Large` with slugs `coding` and `coding-large` plus independent UUID IDs;
- `/v1/models` entries whose IDs are those Instance slugs;
- a stopped Instance whose durable policy survives restart;
- two sibling Instances with different context/GPU/llama.cpp overrides;
- Instance-specific Always On, Autoload and eviction policy;
- a name-only rename that preserves both UUID and slug;
- an explicit Instance slug change that preserves UUID/references and only occurs after an API-breaking warning;
- an explicit Model slug change that preserves Model ID and Instance inference identity;
- API-key Instance scopes that remain valid after Instance slug changes;
- request history that keeps the original model slug after a later Instance rename;
- a failed first-Instance launch without deleting its Model;
- stale PID/port state discarded after manager restart;
- split GGUF artifacts and resumable downloads;
- a versioned GGUF metadata inspection/cache preserving generic key/type/value data;
- manager-derived values such as Context capability without requiring a schema field for every GGUF metadata key;
- cache invalidation when the local artifact/shard fingerprint changes;
- `/models/:slug/details` generic metadata data without adding Instance runtime state to the Model.
