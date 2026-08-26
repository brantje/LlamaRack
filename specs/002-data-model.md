# 002 — Data Model

Status: Draft

Related issue: #1

## 1. Purpose

This document defines the durable domain model for `llamacpp-manager`.

The core separation is:

- **Model** = registered management-plane model configuration;
- **Instance** = durable configured `llama-server` process definition and inference identity;
- **Runtime state** = ephemeral observed process state for an Instance.

SQLite is the durable store for configuration that must survive manager restarts.

## 2. Development schema policy

The project is still in active development.

For Phase 5.5 and related schema restructuring:

- change the current schema directly;
- update fixtures/seeds/tests directly;
- development databases may be recreated;
- **do not create database migration files for this work**.

A production/release migration policy can be introduced before schema backward compatibility becomes a requirement.

## 3. Design principles

- Models and Instances are distinct first-class entities.
- A Model can exist without an Instance.
- Multiple Instances may reference one Model.
- Lifecycle/scheduler state belongs to Instances, not Models.
- Desired Instance configuration is separate from observed runtime state.
- llama.cpp configuration uses inheritance rather than duplicated full configs.
- Instance identity is human-friendly and directly usable by OpenAI-compatible clients.
- High-frequency metrics are not permanently relational by default.
- Secrets remain separate from normal settings.

## 4. Core entities

### 4.1 User / Session / API Key / Secret

These retain the existing v1 security model:

- local management users;
- server-side sessions;
- hashed inference API keys;
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
- architecture/parameter/context metadata where known;
- completion state;
- timestamps.

Split GGUFs remain one logical artifact with multiple Artifact File rows.

### 4.4 Model

A Model is a registered/configured management-plane model.

Conceptual fields:

- `id` — internal stable Model identifier;
- `name` — user-facing Model name;
- `artifact_id`;
- `enabled` if needed for management availability;
- timestamps.

Model-derived summary data may expose:

- backing path;
- size;
- quantization;
- context capability.

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

- Model FK;
- canonical option key;
- normalized serialized value;
- validation/source metadata where useful;
- updated timestamp.

These values are defaults inherited by Instances.

### 4.6 Instance

An Instance is one durable configured potential `llama-server` process.

Conceptual fields:

- `id` — unique slug derived from Instance name;
- `name` — human-entered Instance name;
- `model_id` — FK to registered Model;
- `enabled` where needed;
- `always_on`;
- `autoload_enabled`;
- `eviction_enabled`;
- `priority` — `low`, `normal`, `high`;
- `idle_timeout_seconds` nullable/inherited where supported;
- `startup_timeout_seconds` nullable/inherited where supported;
- GPU assignment mode;
- selected GPU stable identifiers;
- tensor split mode/configuration;
- timestamps.

The Instance row is durable even while no process exists.

### 4.7 Instance identity rules

Instance identity is intentionally derived from its name.

```text
Instance name
   -> slugify
   -> instance.id
   -> OpenAI model identifier
```

Example:

```text
name = "Qwen Coding 32B"
id   = "qwen-coding-32b"
```

Clients use:

```json
{"model":"qwen-coding-32b"}
```

Rules:

- `instance.id` is globally unique among addressable Instances;
- slug generation is deterministic;
- use a conservative URL/JSON-safe character set;
- no separate `public_id`, `model_alias` or inference alias is required;
- renaming an Instance changes its slug/ID;
- rename is therefore API-breaking and requires explicit warning/confirmation in the UI;
- duplicate/colliding slugs must fail validation rather than silently target the wrong Instance.

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

- `instance_id`;
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

### 4.10 Download job / provider cache

Retain the existing durable download-job model and bounded provider cache behavior.

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

## 6. Derived views

### Model summary

For `/models`:

- Model ID/name;
- path;
- size;
- quantization;
- context capability;
- Edit/Delete affordances.

Do not mix Instance runtime state into the Models table.

### Instance summary

For `/instances`:

- Instance `id` and name;
- referenced Model;
- configured lifecycle/resource policy;
- observed runtime state;
- placement summary;
- health/failure information;
- runtime metrics as later phases add them.

## 7. Model creation with optional first Instance

Creating a Model may optionally bootstrap one Instance.

The Model creation UI may collect only these Instance-specific settings:

- Instance name;
- Always On;
- Autoload on request;
- Allow resource-pressure eviction;
- whether to start immediately.

The backend must:

1. create the Model;
2. slugify Instance name to `instance.id`;
3. validate uniqueness;
4. create the Instance;
5. apply the selected three policies;
6. optionally request launch.

If process startup fails, do not delete the successfully created Model or Instance.

## 8. Deletion semantics

- **Delete Instance** — stop/kill as required, then remove the durable Instance definition.
- **Delete Model** — only allowed when dependent Instances are handled explicitly; do not accidentally orphan them.
- **Delete artifact** — separate destructive operation with dependency checks.

Deleting a Model does not implicitly delete a multi-gigabyte artifact unless the user explicitly performs that action.

## 9. Persistence and transactions

Durable multi-row operations should be transactional where practical.

Examples:

- creating a Model plus its optional first Instance definition;
- duplicating an Instance and its override rows;
- updating an Instance name/ID plus related foreign-key references if the schema requires it;
- marking download completion and artifact files.

Worker process startup cannot be inside a SQLite transaction. Persist desired state first, then execute lifecycle actions.

## 10. Rename semantics

Because `instance.id` is derived from Instance name, renaming changes inference identity.

Required behavior:

- compute the new slug before save;
- validate uniqueness;
- warn that clients using the old OpenAI `model` value will break;
- apply rename atomically to durable references where possible;
- do not retain a hidden compatibility alias in v1 unless explicitly added as a future feature.

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

## 13. Invariants

1. A Model references a usable completed artifact.
2. A Model may have zero or many Instances.
3. An Instance references exactly one Model.
4. Lifecycle/scheduler policy belongs to the Instance.
5. `instance.id` is the slug derived from Instance name.
6. `instance.id` is the exact OpenAI `model` identifier.
7. There is no second public Instance alias in v1.
8. Runtime PID/port do not prove liveness after restart.
9. Model and Instance llama.cpp overrides retain inheritance semantics.
10. Instance GPU assignments cannot silently retarget another device.
11. Model deletion and artifact deletion are separate.
12. Phase 5.5 schema changes do not require migration files during development.

## 14. Acceptance criteria

The data model is adequate when it can represent:

- a registered Model with no Instance;
- one Model with two independently configured Instances;
- Instance names `Coding` and `Coding Large` with IDs `coding` and `coding-large`;
- `/v1/models` entries whose IDs are those Instance IDs;
- a stopped Instance whose durable policy survives restart;
- two sibling Instances with different context/GPU/llama.cpp overrides;
- Instance-specific Always On, Autoload and eviction policy;
- a rename that changes `instance.id` only after explicit API-breaking warning;
- a failed first-Instance launch without deleting its Model;
- stale PID/port state discarded after manager restart;
- split GGUF artifacts and resumable downloads;
- no development migration file for the Phase 5.5 schema rewrite.