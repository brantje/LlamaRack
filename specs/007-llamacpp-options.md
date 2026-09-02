# 007 — llama.cpp Configuration and Option Discovery

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how LlamaRack discovers, stores, validates, presents and applies `llama-server` options.

The manager must not require a release whenever llama.cpp adds a flag. The active binary is inspected at runtime and a normalized option schema drives validation and UI rendering.

## 2. Hardware-integration first task

The llama.cpp configuration GUI remains the **first hardware-integration task** before GPU inventory, placement and eviction proceed.

This catch-up task must complete:

- Basic configuration UI from curated option metadata;
- searchable Advanced UI containing every configurable option discovered from `llama-server --help`;
- global defaults;
- Model overrides/defaults;
- Instance overrides;
- backend validation;
- deterministic argv generation;
- effective-value/source display;
- protected manager-owned options;
- integration points for later GPU placement/tensor split controls.

Hardware integration is not complete if the option GUI remains missing.

## 3. Configuration layers

V1 uses three user-controlled layers:

```text
Global llama.cpp defaults
          +
Model overrides/defaults
          +
Instance overrides
          =
Effective Instance configuration
```

### Global defaults

Shared manager-wide defaults.

### Model overrides/defaults

Reusable configuration attached to a registered Model and inherited by every Instance referencing that Model unless overridden.

### Instance overrides

Per-Instance changes allowing sibling Instances of the same Model to differ in context, cache, batch, specialized modes and other supported llama.cpp settings.

There is no named preset inheritance tree in v1.

## 4. Model creation configuration

`/models/new` is the canonical surface for Model-level llama.cpp defaults.

Any **Model-scoped llama.cpp option** available when editing a Model must also be available during Model creation with the same defaults, validation and semantics.

This parity requirement applies only to Model-scoped llama.cpp configuration.

It does **not** mean `/models/new` must expose the full Instance editor.

## 5. Optional first Instance on `/models/new`

Model creation may optionally bootstrap and launch a first Instance.

That first-Instance section intentionally exposes only:

- Instance name;
- Always On;
- Autoload on request;
- Allow resource-pressure eviction;
- launch immediately choice.

Do not expose on `/models/new`:

- Instance llama.cpp overrides;
- GPU selection;
- tensor split;
- priority;
- idle/startup timeout;
- other advanced Instance settings.

Full Instance configuration belongs to `/instances/new` and `/instances/:id/edit`.

## 6. Instance identity

Instance name is slugified to `instance.id`.

That ID is also the OpenAI `model` value.

Changing llama.cpp configuration does not change Instance identity. Renaming the Instance does.

There is no separate inference alias field.

## 7. Goals

The option system must:

- expose every discoverable configurable `llama-server` option in Advanced mode;
- provide curated Basic mode;
- discover from the active binary;
- detect binary changes and refresh schema;
- infer useful types/validation metadata;
- retain unknown/unsupported configured options;
- support Global + Model + Instance layers;
- show effective source/value clearly;
- generate deterministic launch argv;
- validate conflicts before startup where possible;
- allow placement to generate a tensor split while giving an explicit effective llama.cpp `tensor-split` override deterministic precedence over that generated value.

## 8. Non-goals

- named reusable presets beyond Global/Model/Instance inheritance;
- environment-variable-based per-Model/Instance config;
- arbitrary shell command fields;
- runtime hot reload for immutable llama.cpp options;
- remote binary schema discovery.

## 9. Binary discovery

Record active `llama-server`:

- path/identity;
- version/build/commit where available;
- executable fingerprint;
- backend/build flavor where detectable;
- help-output fingerprint.

The option schema is tied to the binary profile.

## 10. Schema refresh triggers

Refresh when:

- no matching schema exists;
- executable fingerprint changes;
- version/build identity changes;
- user requests refresh;
- prior discovery failed and retry is appropriate.

Do not run `--help` before every Instance launch.

## 11. Help parser

Parse where possible:

- long/short names;
- boolean positive/negative forms;
- argument placeholders;
- descriptions;
- defaults;
- enum choices;
- repeatability;
- sections/categories.

Parser uncertainty must not hide the option. Use raw/unclassified representation when needed.

## 12. Normalized option definition

Conceptually:

- canonical key;
- aliases;
- description;
- category;
- inferred type;
- optional default;
- optional allowed/min/max metadata;
- repeatable/value-required metadata;
- boolean polarity metadata;
- curated Basic metadata;
- parser confidence.

## 13. Supported value types

At least:

- boolean;
- integer;
- float;
- string;
- enum;
- string/number lists;
- size/duration-like values where appropriate;
- raw/unclassified.

## 14. Curated metadata

Manager-owned metadata may provide:

- friendly labels;
- help text;
- category;
- Basic visibility;
- recommended control;
- units;
- known incompatibilities;
- restart implications;
- hardware-control mapping.

Curated metadata must never fabricate support missing from the active schema.

## 15. Basic view

Basic mode may include supported common options such as:

- context size;
- GPU layers/offload;
- CPU threads;
- batch/ubatch;
- parallel slots;
- flash attention;
- template/Jinja controls;
- reasoning controls;
- speculative decoding;
- embedding/reranking mode;
- KV/cache settings;
- tensor split.

GPU device assignment remains primarily an Instance placement control. `tensor-split` is also available through the normal llama.cpp option hierarchy so advanced users can override the manager-generated placement split.

## 16. Advanced view

Advanced mode:

- shows every discovered configurable option;
- supports search/category navigation;
- displays canonical CLI spelling;
- shows source: upstream/global/Model/Instance;
- allows adding/removing the override for the current configuration layer;
- marks unsupported-retained values;
- provides raw input for uncertain types;
- shows manager-protected values read-only.

## 17. Effective value/source

For each option distinguish:

- upstream default;
- Global manager value;
- Model override;
- Instance override;
- final effective launch value;
- omitted/unset state.

Explicit value equal to inherited value is still a distinct source state.

## 18. Reset/inheritance

Removing:

- a Model override restores Global inheritance;
- an Instance override restores Model/Global inheritance.

Do not implement reset by copying the current parent value.

Boolean options must represent true/false/unset where necessary.

## 19. Unsupported retained options

If a configured key disappears from a later binary:

- retain it in storage;
- mark unsupported;
- normally exclude it from launch;
- display warning;
- allow removal;
- reactivate when a compatible future schema exposes it again.

Never silently delete configuration because upstream changed.

## 20. Validation

### Schema validation

Type/range/enum/value-required checks.

### Curated semantic validation

Examples:

- mutually exclusive flags;
- companion requirements;
- GPU-only flag on CPU build;
- invalid tensor split/device relationships;
- mode/capability conflicts.

### Runtime validation

llama.cpp remains authoritative for unknown constraints. Startup errors feed diagnostics.

## 21. Command construction

Worker argv is generated deterministically from:

1. manager-owned mandatory values;
2. effective Global + Model + Instance llama.cpp configuration;
3. scheduler/Instance placement-derived values;
4. manager safety/integration flags.

Generate argv arrays directly, never shell command strings.

If the effective llama.cpp configuration contains `tensor-split`, that value takes precedence over any scheduler/Instance placement-derived tensor split. The manager must not emit a second `--tensor-split` argument. Placement-derived tensor split is only a fallback when the effective option hierarchy does not set it.

## 22. Protected options

Manager-owned values may include:

- worker host/interface;
- worker port;
- Model artifact path;
- internal auth;
- required health/metrics integration;
- slots endpoint enablement and per-Instance slot save path (`slots`, `no-slots`, `slot-save-path`);
- device and other placement flags generated by the manager.

If discovered in the generic schema, show protected values read-only/managed rather than allowing conflicts.

`tensor-split` is an explicit exception: it remains user-configurable through Global/Model/Instance llama.cpp options and overrides the manager-generated placement split when present.

## 23. GPU UI integration

GPU assignment and the manager's placement tensor split are first-class Instance placement fields.

The placement representation maps into the same final launch argv. Device selection remains manager-owned. `tensor-split` remains editable in the llama.cpp option hierarchy; when an effective override exists, the placement layer must keep its device selection but suppress its own generated `--tensor-split` value.

Hardware-aware automatic placement remains single-GPU first.

## 24. Configuration fingerprint

The effective Instance fingerprint includes:

- active binary profile;
- artifact identity;
- effective Global/Model/Instance options;
- placement-derived args;
- manager-owned launch behavior affecting semantics.

A running worker records its actual launch fingerprint.

## 25. Save semantics

### Direct Instance edit

Runtime-affecting save on a running Instance:

- show restart confirmation;
- persist;
- drain;
- stop;
- restart automatically.

Do not offer a normal long-lived Save-only path for direct running-Instance edits.

### Model/global edit

A Model/global default change can affect multiple running Instances.

These broader edits may mark affected Instances restart-required and use an explicit impact/restart workflow rather than silently restarting many workers.

## 26. Instance rename

Rename is not an option-system change but affects the Instance configuration surface.

Because Instance name slugifies into `instance.id`, rename changes the OpenAI model ID.

The UI must separately warn about API breakage before saving.

## 27. API representation

Management APIs should expose:

- active binary profile;
- option schema + curated metadata;
- Global values;
- Model overrides;
- Instance overrides;
- effective Instance configuration;
- support/validation state;
- fingerprint/restart state.

The frontend must not duplicate authoritative inheritance logic.

## 28. Security

- execute `llama-server --help` directly, never through shell interpolation;
- generate argv arrays;
- treat upstream help text as untrusted display text;
- prevent overrides of manager-owned bind/port/path controls;
- keep secrets outside the option system;
- bound option value sizes.

## 29. Testing

Maintain parser fixtures across llama.cpp versions and cover:

- boolean flags;
- aliases;
- numeric/string/enum values;
- repeated values;
- unknown/raw options;
- removed/new options;
- curated merge;
- Global/Model/Instance inheritance;
- protected options;
- explicit `tensor-split` precedence over placement-generated tensor split;
- deterministic argv;
- sibling Instances with different overrides.

## 30. Invariants

1. Advanced mode never intentionally hides a discovered configurable option.
2. Global + Model + Instance layers remain distinguishable.
3. Instance overrides can differ between sibling Instances.
4. Removing an override restores inheritance.
5. Manager-owned launch values cannot be overridden unsafely.
6. Generated argv is deterministic for the same effective Instance configuration.
7. Direct running-Instance edits confirm and automatically restart when required.
8. `/models/new` exposes all Model-scoped llama.cpp options but only the small three-policy first-Instance bootstrap.
9. Instance identity remains the slug-derived `instance.id`, independent from llama.cpp option values.
10. An effective llama.cpp `tensor-split` value suppresses any manager-generated tensor split while leaving manager device placement in control.
11. No shell construction is required.

## 31. Acceptance criteria

- active binary help is parsed;
- unknown new flag appears in Advanced mode without dedicated backend code;
- curated flags appear in Basic mode;
- Global defaults flow into Models/Instances;
- Model overrides flow into sibling Instances;
- Instance override changes only that Instance;
- removing Instance override restores Model inheritance;
- unsupported values survive binary change;
- protected port/bind/model-path options cannot be overridden;
- `tensor-split` is editable in llama.cpp options and its effective Global/Model/Instance value takes precedence over placement-generated tensor split;
- effective configuration yields deterministic argv/fingerprint;
- `/models/new` provides Model llama.cpp config plus only Instance name/Always On/Autoload/Eviction/launch bootstrap fields;
- `/instances/new` and edit expose full Instance override/placement configuration;
- running Instance save confirms and restarts automatically.