# 007 — llama.cpp Configuration and Option Discovery

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how llamacpp-manager discovers, stores, validates, presents and applies `llama-server` configuration options.

The central design requirement is that the manager must not require a new release every time llama.cpp adds a command-line flag. The active binary is inspected at runtime, while curated metadata improves the experience for common options.

### 1.1 Delivery requirement — first task in Phase 7

The llama.cpp configuration GUI was originally a Phase 3 deliverable and remains required before Phase 7 hardware integration proceeds.

**The first implementation task in Phase 7 must be to complete the missing llama.cpp options GUI and its end-to-end configuration path.** Hardware telemetry, automatic GPU placement, visual GPU assignment and tensor-split work must not be treated as the first Phase 7 implementation work while this configuration surface is incomplete.

This Phase 7 catch-up task includes, at minimum:

- render the curated **Basic** llama.cpp configuration view from the discovered/curated option schema;
- render the searchable **Advanced** view containing every configurable option discovered from the active `llama-server --help` schema;
- support global defaults and per-model overrides with explicit inherit/unset semantics;
- expose model-level llama.cpp configuration both when editing an existing model and on `/models/new` in the same implementation, preserving model-creation configuration parity;
- validate values and conflicts through the backend configuration engine rather than duplicating authoritative validation in the frontend;
- generate the effective deterministic worker argv from the saved configuration;
- expose effective values/source and restart-required state where applicable;
- ensure manager-owned/protected options remain non-editable while derived placement options can later be integrated with the Phase 7 GPU UI.

Phase 7 hardware integration may build on this configuration surface immediately afterward, especially for GPU layers/offload, device selection, tensor split, context/KV settings and other options that affect VRAM placement. Phase 7 is not considered complete if the Basic/Advanced llama.cpp configuration GUI or `/models/new` parity remains missing.

## 2. Goals

The option system must:

- expose every discoverable `llama-server` option in Advanced configuration;
- provide a curated Basic view for commonly used options;
- discover options from the active `llama-server --help` output;
- detect binary/version changes and refresh the schema;
- infer useful value types and validation metadata;
- preserve options the manager does not yet understand;
- support global defaults plus per-model overrides;
- render effective configuration clearly;
- generate deterministic launch arguments;
- validate conflicting/unsupported settings before process startup where possible;
- allow visual GPU/tensor-split controls without hiding their underlying llama.cpp configuration.

## 3. Non-goals for v1

- named reusable model presets beyond global defaults;
- host-level inheritance because v1 is single-host;
- environment-variable-based per-model configuration;
- automatic editing of arbitrary llama.cpp config files;
- runtime hot-reload of options that llama.cpp itself cannot change live;
- remote binary schema discovery.

## 4. Configuration layers

V1 has two user-controlled layers:

```text
Global llama.cpp defaults
          +
Per-model overrides
          =
Effective model configuration
```

Instance definitions may add placement-specific settings such as selected GPUs/tensor split, but ordinary inference/server options belong to the model unless a documented exception exists.

There is no general Model -> Preset -> Host -> Instance inheritance graph in v1.

## 5. Binary discovery

At manager startup, identify the active `llama-server` executable.

Record at least:

- path/identity;
- version string if available;
- build/commit information if available;
- executable fingerprint/hash where practical;
- backend/build flavor where detectable;
- help-output fingerprint.

The option schema is tied to this binary profile.

## 6. Schema refresh triggers

Run/re-run option discovery when:

- no schema exists for the active binary;
- executable fingerprint changes;
- version/build identity changes;
- user explicitly requests capability refresh;
- previous discovery failed and retry policy permits.

Do not execute `--help` before every model start.

## 7. Help parser

The parser consumes `llama-server --help` and creates normalized definitions.

It should recognize, where possible:

- long option names;
- short aliases;
- positive/negative boolean forms;
- argument placeholder(s);
- description text;
- default values stated in help;
- enumerated choices;
- repeatable/multiple values;
- environment references shown by upstream, without making env vars a manager configuration mechanism;
- sections/categories in help output.

The parser must be tolerant of formatting changes. A failure to perfectly infer a type must not cause the option to disappear.

## 8. Normalized option definition

Each option definition conceptually contains:

- canonical key;
- CLI spellings/aliases;
- description;
- category;
- inferred data type;
- optional default;
- optional min/max or allowed values when discoverable;
- repeatable flag;
- value required/optional metadata;
- boolean polarity metadata;
- curated/basic metadata;
- parser confidence;
- raw source fragment or diagnostic metadata where useful.

## 9. Supported value types

The normalized schema should support at least:

- boolean;
- integer;
- float;
- string;
- enum/string choice;
- list of strings;
- list of numbers;
- size-like values where upstream accepts units;
- duration-like values where upstream accepts units;
- raw/unclassified.

When inference is uncertain, use `raw` rather than forcing an incorrect type.

## 10. Curated metadata layer

Runtime discovery provides breadth; a manager-owned curated layer provides UX quality.

Curated metadata may define:

- friendly label;
- explanation/help text;
- category;
- Basic vs Advanced visibility;
- recommended UI control;
- units;
- known incompatibilities;
- known restart implications;
- mapping to visual hardware controls;
- safe suggested values.

Curated metadata is keyed by canonical option identity and should degrade gracefully if a flag is absent in the active binary.

It must never fabricate support for an option that the active schema does not expose.

## 11. Basic configuration view

The Basic view should prioritize commonly adjusted settings, potentially including when supported by the active binary:

- context size;
- GPU layers/offload;
- GPU selection;
- tensor split;
- CPU threads;
- batch size;
- ubatch size;
- parallel slots;
- flash attention;
- chat template / Jinja controls;
- reasoning-related controls;
- speculative decoding controls;
- embedding/reranking mode;
- KV/cache settings;
- commonly needed server behavior.

The exact list is maintained by curated metadata rather than hard-coded frontend forms scattered throughout the application.

## 12. Advanced configuration view

Advanced configuration lists every option exposed by the current discovered schema, grouped by category where possible.

Requirements:

- searchable by flag name and description;
- show upstream CLI flag spelling;
- show effective value and source (global/default/model);
- permit adding/removing a per-model override;
- expose unclassified/new options rather than hiding them;
- clearly mark options unsupported by the active binary but retained in stored config;
- provide raw input for values whose type cannot be confidently inferred.

## 13. Effective value model

For every option, the UI/API should be able to distinguish:

- upstream/binary default where known;
- manager global override/default;
- model override;
- effective launch value;
- whether the option is omitted entirely from the generated CLI.

An inherited value and an explicitly set value equal to the same number are semantically different for configuration management. Preserve source information.

## 14. Explicit unset/reset

A model override can be removed to inherit the global/default behavior again.

Do not model “reset” by copying the current global value into the model; that would block future global changes from flowing through.

For flags where llama.cpp has explicit positive and negative forms, the normalized representation must be able to express true, false and unset/inherit distinctly.

## 15. Unsupported retained options

If a configured option is absent after changing llama.cpp versions:

- retain the stored override;
- mark it `unsupported` for the active binary;
- exclude it from worker launch by default;
- show a clear warning/restart/config invalid state according to severity;
- allow the user to remove it;
- automatically reactivate it if a later binary exposes the same canonical option again and validation succeeds.

Never silently delete user configuration because upstream removed/renamed a flag.

## 16. Option aliases and canonical keys

Different help versions may expose aliases or renamed spellings.

Use one canonical internal key per discovered option definition.

Curated metadata may define known alias/rename mappings, but automatic migrations must be conservative. If two flags may differ semantically, keep them separate and surface migration guidance rather than guessing.

Generated launch arguments should prefer the active binary's canonical long form for readability/logging.

## 17. Validation layers

Validation occurs at multiple levels.

### Schema validation

Examples:

- integer expected;
- enum value allowed;
- required value present.

### Curated semantic validation

Examples:

- mutually exclusive options;
- required companion options;
- GPU-only flag used in CPU build;
- invalid tensor split length for selected devices;
- embedding mode inconsistent with intended model API use where known.

### Runtime validation

llama.cpp remains authoritative for constraints the manager cannot know. Startup errors are captured and mapped back to configuration diagnostics when possible.

## 18. Raw CLI escape hatch

The requirement is “friendly basic UI + advanced raw options,” not an unrestricted shell command field.

The Advanced UI should expose raw/discovered **option values**, but avoid a generic arbitrary command-line text box that can inject unknown process arguments or shell syntax.

If a future “extra args” feature is added, it must be parsed as argv tokens without shell execution and clearly marked unsupported/unvalidated.

V1 should not require it because all detected options are already visible.

## 19. Command construction

Worker launch arguments are generated deterministically from:

1. manager-owned mandatory worker arguments (model path, private bind/port, etc.);
2. effective global/model llama.cpp configuration;
3. instance placement/GPU settings;
4. manager-owned safety/internal flags where required.

User configuration must not override manager-critical settings in ways that expose workers publicly or collide with assigned ports.

Manager-owned options therefore have protected status.

## 20. Protected options

Examples of values the manager may need to own:

- worker listen host/interface;
- worker port;
- model path selected from the model artifact;
- internal auth key if used;
- metrics/health flags required for manager functionality;
- process/logging integration settings required for supervision.

If the same llama.cpp option appears in the general schema, the UI should explain that the manager controls it and make it read-only/hidden from ordinary per-model override editing.

## 21. GPU UI integration

Visual GPU settings must map into the same effective launch configuration rather than creating a parallel conflicting path.

The configuration service exposes a normalized placement representation to the scheduler and converts it into the appropriate active llama.cpp arguments.

If a user changes the corresponding raw Advanced option, the UI should either:

- update the visual representation if safely parseable; or
- declare one source authoritative and prevent conflicting edits.

Preferred design: GPU assignment/tensor split are first-class instance placement fields; corresponding protected/generated CLI flags are shown as derived/read-only in Advanced view.

## 22. Configuration fingerprints

Generate a deterministic effective configuration fingerprint including:

- active binary profile;
- artifact identity;
- effective launch option values;
- relevant instance placement-derived args;
- manager-required launch behavior that affects worker semantics.

A running worker stores its launch fingerprint.

If current desired fingerprint differs, expose `restart_required`.

## 23. Save semantics

Saving model configuration updates durable desired state.

The API/UI should support:

- Save only — persist changes and mark running instances restart-required;
- Save and restart — persist, then perform a controlled restart;
- Cancel/discard unsaved frontend changes.

Do not automatically restart a large running model on every field edit.

## 24. Global default changes

When a global default changes:

- models without an override inherit the new effective value;
- recompute fingerprints;
- affected running instances become restart-required;
- models with their own override remain unchanged for that option.

The UI should be able to preview how many configured/running models are affected before applying broad global changes where practical.

## 25. API representation

Management API should expose:

- active llama.cpp binary profile;
- discovered option schema;
- curated metadata merged for presentation;
- global default values;
- model overrides;
- effective configuration;
- validation warnings/errors;
- restart-required status.

Avoid forcing the frontend to reproduce inheritance and support logic itself.

## 26. Security

- Run `llama-server --help` as a direct executable call, never through a shell with user-supplied interpolation.
- Generate argv arrays, not shell command strings.
- Treat all descriptions/help text as untrusted display text in the browser.
- Prevent model options from overriding manager-owned bind/port/path safety.
- Do not expose secret values through the options system.
- Bound stored option value sizes.

## 27. Failure handling

### Discovery fails

Keep the last schema only if it is positively tied to the same binary fingerprint; otherwise expose degraded configuration capability and block unsafe starts requiring unknown validation as appropriate.

### Parser cannot understand one option

Store it as raw/unclassified and expose it in Advanced mode.

### Generated args rejected by llama.cpp

Capture startup logs, classify invalid configuration where possible, and make the relevant effective values visible for troubleshooting.

### Binary changes while workers run

Existing workers remain associated with their launch profile. New starts use the active profile. Mark affected configurations/restarts clearly; do not imply already-running processes changed binary semantics.

## 28. Testing strategy

Maintain parser fixtures from multiple llama.cpp help versions.

Tests should cover:

- boolean flags;
- flags with aliases;
- numeric/string values;
- enum-like descriptions;
- repeated values;
- sections/categories;
- formatting changes;
- unknown/raw options;
- removed options;
- newly introduced options;
- curated metadata merge;
- protected manager-owned options;
- deterministic argv generation.

Do not make CI depend exclusively on the newest network-downloaded llama.cpp binary; keep fixtures for reproducibility.

## 29. Invariants

1. Advanced view never intentionally hides a successfully discovered configurable option.
2. New upstream flags can appear without adding a dedicated backend field.
3. User overrides are not silently deleted when a binary changes.
4. Manager-owned bind/port/model-path settings cannot be overridden by model config.
5. Global defaults and model overrides remain distinguishable.
6. Removing an override restores inheritance.
7. Generated worker args are deterministic for the same effective configuration.
8. Running workers retain the fingerprint/profile they actually launched with.
9. Configuration changes that require restart are not presented as already active.
10. No shell command construction is required to launch llama.cpp.

## 30. Acceptance criteria

The feature is complete when:

- manager discovers the active binary and parses its help output;
- a newly discovered unknown flag appears in Advanced configuration without manager code specific to that flag;
- curated common flags appear in Basic configuration with improved labels/help;
- global defaults flow into models without overrides;
- per-model overrides replace inherited values;
- removing an override restores inheritance;
- unsupported retained options survive a binary profile change and are clearly flagged;
- manager-owned port/bind/model-path options cannot be changed by a model;
- visual GPU settings generate consistent protected llama.cpp arguments;
- effective configuration produces a deterministic launch fingerprint;
- changing an effective option marks a running instance restart-required;
- generated argv is passed directly to the child process without shell interpolation.