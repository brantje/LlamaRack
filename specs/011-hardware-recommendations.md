# 011 — GGUF Metadata and Hardware Recommendations

Status: Draft

Related issues: #1, #25

Target branch: `feature/phase-9-hardware-recommendations`

## 1. Purpose

Phase 9 should turn the existing small GGUF reader into a reusable GGUF inspection service.

The same inspector is used by:

- local Model creation;
- Hugging Face post-download Model registration;
- Context capability detection;
- hardware recommendations;
- `/models/:id/details`.

The inspector must read GGUF metadata without starting `llama-server` or loading model tensor payloads into RAM/VRAM.

## 2. Existing Phase 9 baseline

The branch already contains:

- `backend/internal/recommendations`;
- `GET /api/v1/models/:id/recommendation`;
- context-sensitive RAM/VRAM/KV estimates;
- single-GPU-first recommendation integration;
- hardware-fit UI in Instance placement and Hugging Face discovery.

The current GGUF reader lives inside the recommendations package and extracts only a small set of fields. Move this into shared backend code rather than adding more feature-specific parsers.

## 3. Shared GGUF inspector

Create a reusable package, conceptually:

```text
backend/internal/ggufmeta
```

Its primary job is simple: read the GGUF header and expose the metadata contained in the file.

Conceptually:

```text
Inspection
  version
  metadata[]
    key
    type
    value
  tensor_count
  warnings[]
```

The raw GGUF metadata is the important part. Do not require a hard-coded product model for every possible metadata key.

Unknown/new GGUF keys must still be returned and displayed.

## 4. Metadata parsing

The reader must:

- validate GGUF magic/version;
- read all GGUF key/value metadata types supported by the format;
- preserve key names and value types;
- handle arrays safely;
- use defensive size/count limits;
- avoid reading tensor payload data merely to inspect metadata;
- return useful warnings for malformed or unsupported data;
- support split GGUFs by reading metadata from the appropriate primary shard and existing logical artifact information.

Some metadata values, especially tokenizer arrays, can be very large. The API must not require returning every large array value in one response. Large values may be summarized initially and expanded/paged on demand.

## 5. Product-derived fields

The manager may derive a small number of fields from the generic metadata when needed by existing product features.

The first required derived field is Context capability for issue #25:

```text
general.architecture = qwen2
qwen2.context_length = 32768

-> Context capability = 32768
```

Use `general.architecture` to resolve the matching architecture-specific context-length key instead of assuming `llama.context_length`.

The recommendation engine may also read whichever metadata values it needs for better RAM/VRAM/KV estimates. Those derived values belong in recommendation logic; they do not require dedicated sections on the Model details page.

Avoid building a large hard-coded catalogue of architecture/tokenizer/MoE/UI fields unless a concrete product feature needs one.

## 6. Tensor information

Phase 9 may inspect the GGUF tensor directory when it materially improves hardware recommendations, for example to derive parameter counts or more accurate weight/layer sizing.

This remains an internal recommendation input and is separate from the requirement for `/models/:id/details`.

The details page is primarily a **GGUF metadata viewer**. It does not need a specialized tensor explorer unless that is added as a separate future requirement.

Tensor payloads must never be read merely to inspect tensor descriptors.

## 7. Model creation

When a user selects an existing local GGUF on `/models/new`:

1. validate the path;
2. run the shared GGUF inspector;
3. derive Context capability when available;
4. pre-fill Context capability before save;
5. keep the value editable;
6. show a non-blocking warning if detection fails;
7. preserve an explicitly entered value when detection fails.

Prefer a pre-save inspection endpoint, conceptually:

```text
POST /api/v1/models/inspect
{
  "gguf_path": "..."
}
```

The response should contain the useful detected summary plus warnings. The frontend does not need the complete metadata set just to create a Model.

## 8. Hugging Face integration

After a Hugging Face GGUF is completely downloaded:

```text
download complete
-> shared GGUF inspection
-> derive Context capability / recommendation inputs
-> Model registration flow
```

Do not create separate Hugging Face-specific GGUF parsing logic.

Split GGUF downloads must remain one logical artifact.

## 9. Model details API

Add a management endpoint for registered Model details, conceptually:

```text
GET /api/v1/models/:id/details
```

It should return:

- Model name/id/path;
- file/logical artifact size;
- GGUF version;
- metadata count;
- inspection warnings;
- a bounded page/list of GGUF metadata entries.

Each metadata entry should expose at least:

```text
key
type
value
```

Support search/filter and pagination where useful. Large array/string values may have a separate expand/read operation so the initial response remains bounded.

The API must expose **all metadata keys**, including keys the manager does not understand.

## 10. `/models/:id/details` UI

Add:

```text
frontend/app/pages/models/[id]/details.vue
```

Add **Details** to the `/models` row actions alongside Edit/Delete.

Keep the page generic.

At the top show a small Model/GGUF summary such as:

- Model name;
- path;
- size;
- GGUF version;
- architecture if present;
- quantization if already known;
- Context capability;
- metadata count;
- warnings.

The main content is a searchable GGUF metadata table:

| Key | Type | Value |
| --- | --- | --- |
| `general.architecture` | string | `qwen2` |
| `qwen2.context_length` | uint32 | `32768` |
| `tokenizer.ggml.model` | string | `gpt2` |
| ... | ... | ... |

Requirements:

- show every metadata key from the GGUF;
- sort consistently, preferably by key;
- allow search/filter by key;
- display scalar values directly;
- display arrays/long values in a compact form with expansion;
- provide copy affordances where practical;
- remain usable for GGUF files with lots of metadata;
- use Nuxt UI components first according to `AGENTS.md`.

Do **not** make dedicated required tabs for Architecture, MoE, Tokenizer, FIM, Vision, RoPE, etc. Those values naturally appear in the metadata table when they exist.

Do not put Instance lifecycle/runtime controls on this page.

## 11. Metadata caching

The GGUF file remains the source of truth.

A versioned cache may be used so every page load does not have to re-read the file. Cache invalidation should use enough artifact information to notice that the file/shard set changed.

Do not create a database column for every metadata key.

During active development, update the current schema directly if cache storage needs schema changes; no migration files are required for this work.

## 12. Recommendation integration

Refactor `internal/recommendations` to consume the shared inspector rather than owning GGUF parsing.

Continue improving estimates opportunistically from available metadata, including context and KV-related architecture values where present.

If tensor-directory inspection is added, recommendations may use it for more accurate weight/offload sizing.

Recommendations remain estimates and must degrade gracefully when metadata is absent.

## 13. Implementation slices

### Slice 9A — Shared generic GGUF metadata reader

- create `internal/ggufmeta`;
- move binary parsing out of recommendations;
- expose all key/type/value metadata;
- add defensive limits and malformed-file tests;
- support bounded handling of large values.

### Slice 9B — Context capability integration

- implement architecture-aware `*.context_length` detection;
- add `/models/inspect`;
- auto-fill `/models/new`;
- integrate Hugging Face post-download registration;
- cover issue #25.

### Slice 9C — Generic Model details

- add `GET /api/v1/models/:id/details`;
- add bounded metadata listing/expansion;
- add `/models/:id/details`;
- add Details action on `/models`;
- implement searchable Key / Type / Value metadata UI.

### Slice 9D — Recommendation refactor/improvements

- make recommendations consume `ggufmeta`;
- use additional metadata only where useful for estimates;
- optionally inspect tensor descriptors for parameter/weight/layer calculations;
- retain single-GPU-first scheduler behavior.

### Slice 9E — Quality pass

- local/split/corrupt GGUF fixtures;
- backend and frontend behavior/error tests;
- authorization coverage;
- large-value handling tests;
- 90% coverage gates;
- formatter/linter/type/build checks.

## 14. Acceptance criteria

- [ ] GGUF parsing is shared backend functionality rather than recommendation-specific code.
- [ ] The inspector exposes all GGUF metadata keys with their types and values.
- [ ] Unknown metadata keys remain visible without code changes.
- [ ] Inspection does not start `llama-server` or load tensor payloads.
- [ ] Large metadata values/arrays are handled in a bounded manner.
- [ ] Split GGUF metadata inspection works through the logical artifact/primary shard.
- [ ] Context capability is detected via `general.architecture` + the matching architecture-specific context-length key.
- [ ] `/models/new` auto-fills detected Context capability and keeps it editable.
- [ ] Detection failures are non-blocking and preserve explicitly entered values.
- [ ] Hugging Face post-download Model registration uses the same inspector.
- [ ] Recommendations consume the shared inspector.
- [ ] `/models/:id/details` exists and is reachable through a Details action on `/models`.
- [ ] The details page shows a small Model/GGUF summary and a generic searchable metadata Key / Type / Value view.
- [ ] The details page does not require specialized sections for particular architectures or capabilities.
- [ ] Every GGUF metadata key can be inspected, including manager-unknown keys.
- [ ] Model details contain no Instance lifecycle/runtime controls.
- [ ] Invalid/malformed metadata produces a warning rather than crashing unrelated Model management.
- [ ] Backend and frontend coverage remain at or above the repository's 90% gates.
