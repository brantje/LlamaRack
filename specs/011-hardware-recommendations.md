# 011 — GGUF Metadata and Hardware Recommendations

Status: Draft

Related issues: #1, #25

Target branch: `feature/phase-9-hardware-recommendations`

## 1. Purpose

Phase 9 turns GGUF inspection into a shared model capability rather than a recommendation-only helper.

The manager must inspect a GGUF without starting `llama-server` or loading tensor payloads, normalize useful architecture facts, expose all GGUF metadata for inspection, and use the same source of truth for:

- automatic Model metadata on local add;
- post-download Hugging Face registration;
- Context capability detection;
- quantization and parameter information;
- RAM/VRAM and KV-cache estimates;
- recommended GPU offload;
- Model details UI.

The registered Model details route is:

```text
/models/:id/details
```

This page is management-plane model information only. Runtime lifecycle state and controls remain on `/instances`.

## 2. Existing Phase 9 baseline

The Phase 9 branch already contains:

- `backend/internal/recommendations`;
- `GET /api/v1/models/:id/recommendation`;
- context-sensitive RAM/VRAM/KV estimates;
- single-GPU-first recommendation integration with the scheduler placement planner;
- hardware-fit guidance in Instance placement and Hugging Face discovery.

The current GGUF reader is intentionally small and embedded in the recommendations package. It extracts only a limited set of architecture fields.

Do not grow that parser in place. Replace it with a reusable GGUF inspection package and make recommendations a consumer of the normalized inspection result.

## 3. Design principles

1. **One parser, many consumers.** Local add, Hugging Face import, details, and recommendations must not implement separate GGUF parsing rules.
2. **Header-only inspection.** Metadata and tensor descriptors may be read, but tensor payload bytes must not be loaded merely for inspection.
3. **Architecture-aware normalization.** Resolve architecture metadata from `general.architecture`; do not assume `llama.*` keys.
4. **Preserve raw data.** Derived fields are convenient summaries, not a replacement for raw GGUF key/value metadata.
5. **Do not create a DB column for every GGUF key.** Persist a compact normalized summary plus a versioned raw-metadata cache/fingerprint where useful.
6. **Exact facts and estimates stay distinct.** File sizes, tensor shapes, parameter counts and GGUF values are facts. Runtime overhead and fit recommendations are estimates and must be labelled accordingly.
7. **Split GGUF is one logical model artifact.** Read each required shard header/tensor table where aggregation requires it, without scanning tensor payloads.
8. **Graceful degradation.** Unsupported or malformed metadata should lower confidence and show warnings; it must not make an otherwise usable GGUF impossible to register.
9. **Bounded APIs/UI.** Some GGUF values such as tokenizer arrays can be enormous. The details page must provide access to all metadata without returning multi-megabyte arrays in the initial response.

## 4. Shared GGUF inspector

Create a dedicated backend package, conceptually:

```text
backend/internal/ggufmeta
```

Move the low-level GGUF parsing logic out of `internal/recommendations`.

The package should expose a logical result similar to:

```text
Inspection
  Format
    version
    metadata_count
    tensor_count
    alignment
    split information

  RawMetadata
    ordered keys
    original GGUF value type
    scalar value or lazy array descriptor

  Architecture
    architecture
    context_length
    block_count
    embedding_length
    feed_forward_length
    attention head counts
    key/value dimensions
    sliding-window configuration
    rope configuration
    expert/MoE configuration

  Tokenizer
    model
    vocabulary size
    BOS/EOS/PAD/etc token ids
    chat template metadata
    FIM token availability

  Modalities
    text
    vision/projector indicators
    audio indicators
    other recognized capability metadata

  Tensors
    name
    dimensions/shape
    ggml type
    element count
    encoded byte span when derivable
    shard
    offset

  Derived
    parameter_count
    tensor_type_distribution
    quantization summary
    per-layer encoded bytes
    non-layer encoded bytes
    model capability flags
```

Use strongly typed normalized fields for product logic while retaining the raw metadata representation for diagnostics/details.

## 5. GGUF parsing requirements

Support the GGUF versions accepted by the current llama.cpp-compatible model set used by the project.

The parser must:

- validate GGUF magic/version;
- use bounded counts and lengths before allocating;
- support all GGUF metadata scalar types required by the format;
- support metadata arrays without eagerly materializing unbounded arrays;
- preserve the original GGUF metadata type;
- read tensor descriptors and shapes;
- calculate safe element counts with overflow checks;
- reject malformed lengths/counts cleanly;
- never trust a metadata count, string length, array length or tensor dimension blindly;
- not seek/read tensor payload data for metadata inspection.

Unknown metadata keys are valid and must remain visible in raw metadata.

## 6. Architecture-specific metadata resolution

Use:

```text
general.architecture = <arch>
```

as the primary prefix for normalized architecture keys.

For example:

```text
general.architecture = qwen2
qwen2.context_length = 32768
qwen2.block_count = ...
qwen2.attention.head_count = ...
```

Context capability resolution must prefer exactly:

```text
<general.architecture>.context_length
```

A suffix-based fallback may be used only when the exact architecture-prefixed value is absent. If multiple plausible values conflict, return a warning instead of silently selecting an arbitrary key.

The same exact-prefix-first rule applies to architecture dimensions used by the estimator where practical.

## 7. Normalized metadata to derive

### 7.1 Identity and provenance

Where available expose:

- GGUF version;
- `general.architecture`;
- model/general name;
- author/organization;
- description;
- license/license name/link;
- source/repository URLs and related provenance fields;
- basename/local artifact path separately from embedded metadata.

Embedded provenance is informational and untrusted; escape it in the UI.

### 7.2 Context and architecture

Normalize where present:

- native/max context capability;
- block/layer count;
- embedding length;
- feed-forward length;
- attention head count;
- KV head count;
- attention key length;
- attention value length;
- sliding-window size/pattern;
- RoPE dimensions/base/scaling metadata;
- architecture-specific recurrent/state-space fields when recognized.

Unknown architecture-specific keys remain in raw metadata even if no normalized field exists.

### 7.3 MoE

Detect and expose where available:

- total expert count;
- experts used per token;
- shared expert count;
- other recognized expert-routing dimensions.

Keep **total parameters** separate from any derived/estimated active-parameters-per-token concept.

### 7.4 Tokenizer and chat

Expose summarized tokenizer facts such as:

- tokenizer model/type;
- vocabulary size;
- BOS/EOS/PAD/UNK and other special token IDs where present;
- chat template presence and names where supported;
- FIM prefix/suffix/middle/pad token presence.

A bundled chat template indicates that template metadata exists. It does not by itself prove every OpenAI tool/reasoning behavior is supported.

### 7.5 Modalities / companion capability

Detect recognized GGUF metadata indicating capabilities such as:

- vision encoder/projector;
- audio encoder;
- embedded MTP/draft-related capability where reliable metadata exists.

Do not infer a capability from an arbitrary filename when authoritative GGUF metadata contradicts it.

Existing Phase 8 sidecar relationships for separate `mmproj` and `mtp-*` files remain valid and distinct from embedded capabilities.

## 8. Tensor table analysis

Inspect tensor descriptors across the logical GGUF artifact.

Derive:

### Parameter count

```text
parameter_count = Σ product(tensor.shape)
```

Use overflow-safe arithmetic.

For split GGUF, aggregate each tensor exactly once across all shards.

### Tensor type / quantization composition

Group tensors by GGML type and expose:

- tensor count per type;
- parameter count per type;
- encoded bytes per type where derivable;
- percentage composition.

Use GGUF `general.file_type` as the primary declared file type when present, but tensor composition is the more detailed description. Filename quantization parsing becomes a fallback, not the primary source after successful inspection.

### Per-layer encoded size

Group recognized transformer layer/block tensor names and calculate encoded bytes per layer where the GGUF type/shape makes the size derivable.

Keep:

- ordered layer/block byte estimates/facts;
- non-layer tensor bytes;
- output/embedding tensor bytes where separable;
- unclassified tensor bytes.

Do not assume every architecture uses identical tensor naming. Implement architecture-aware or pattern-based classification with an explicit unclassified bucket.

## 9. Split GGUF behavior

A split model is one logical inspection.

Requirements:

- identify split metadata from GGUF metadata and/or the existing artifact grouping;
- use the primary/first shard for global metadata when appropriate;
- inspect the tensor table of every shard needed for parameter/tensor/per-layer aggregation;
- never read every tensor payload merely to aggregate descriptors;
- validate duplicate/missing tensor/shard information where detectable;
- preserve shard identity on individual tensor descriptors;
- aggregate total encoded model bytes from the logical artifact, not only the primary shard.

A missing shard should degrade/flag aggregate calculations rather than invent a complete parameter/tensor picture.

## 10. Metadata persistence and cache

The raw GGUF file remains the source of truth.

Do not add one relational column for each raw GGUF key.

Store only product-level fields that are queried frequently as dedicated fields where appropriate, for example:

- context capability;
- architecture;
- parameter count;
- declared/detected quantization summary.

Use a versioned metadata cache for the richer inspection result. The cache should be associated with the Model's logical artifact and include enough fingerprint information to determine whether it is stale, for example:

- logical artifact path/shard set;
- size(s);
- modification time(s) where trustworthy;
- checksum/source revision when available;
- inspector schema/version.

During active development, update the current schema directly; do not add migration files merely for Phase 9 schema changes.

If cached metadata is missing/stale, the details/recommendation path may re-inspect the file header and refresh the cache.

## 11. Automatic metadata during Model creation

Issue #25 is part of this Phase 9 plan.

When adding an existing local GGUF on `/models/new`:

1. resolve and validate the path;
2. inspect GGUF metadata before save;
3. pre-fill Context capability from the architecture-specific context key;
4. pre-fill/derive quantization where inspection provides a stronger value than filename parsing;
5. make detected user-facing values editable;
6. display non-blocking warnings for unavailable metadata;
7. preserve an explicitly entered value if later automatic detection fails;
8. save the accepted Model plus metadata cache/normalized summary.

The scan must not launch `llama-server`.

For the pre-save workflow, expose a dedicated inspection endpoint rather than forcing the frontend to create a Model simply to inspect it. Prefer a POST body for a local path so paths do not need to be placed in query strings/logs.

Conceptually:

```text
POST /api/v1/models/inspect
{
  "gguf_path": "..."
}
```

The API should return editable summary fields plus warnings, not the entire potentially huge raw metadata set unless explicitly requested.

## 12. Hugging Face integration

After a Hugging Face GGUF download becomes complete and locally available:

```text
download complete
-> validate shard completeness
-> shared GGUF inspection
-> persist/refine artifact metadata
-> open/register Model flow with detected defaults
```

The same inspector used by manual Model add must supply Context capability and normalized metadata.

Do not maintain separate Hugging Face-specific parsing logic for context, architecture, parameter count or quantization.

Pre-download recommendations may continue using provider/file metadata with lower confidence. Post-download recommendations should automatically become richer after local GGUF inspection.

## 13. Model details API

Add a management API for the registered Model details page.

Recommended shape:

```text
GET /api/v1/models/:id/details
```

The initial response should include:

- Model identity/path/artifact size;
- normalized GGUF summary;
- inspection warnings/confidence;
- derived parameter/quantization/tensor summaries;
- split/shard summary;
- metadata key inventory/count;
- tensor count;
- recommendation summary or link to the existing recommendation endpoint.

Do not eagerly serialize giant metadata arrays or every tensor descriptor into this initial response.

Provide bounded/lazy access for all raw metadata and tensors, conceptually:

```text
GET /api/v1/models/:id/metadata?prefix=&cursor=&limit=
GET /api/v1/models/:id/tensors?query=&cursor=&limit=
```

The exact endpoint names may differ, but the contract must support:

- search/filter by metadata key;
- all raw metadata keys;
- typed scalar values;
- bounded slices of large array values;
- pagination/expansion for huge arrays;
- tensor search/filter;
- paginated tensor descriptors;
- no arbitrary unbounded response size.

For integer values that can exceed JavaScript's exact integer range, preserve the exact value rather than silently rounding it in JSON. A typed/string representation is acceptable for the raw metadata API.

## 14. `/models/:id/details` UI

Add:

```text
frontend/app/pages/models/[id]/details.vue
```

Add **Details** as a Model inventory action alongside Edit/Delete.

The page should use Nuxt UI components first and remain consistent with `AGENTS.md`.

Recommended sections/tabs:

1. **Overview**
   - name/path;
   - total artifact size;
   - architecture;
   - parameter count;
   - quantization;
   - context capability;
   - split/shard state;
   - metadata confidence/warnings.

2. **Architecture**
   - layers/blocks;
   - embedding/FFN dimensions;
   - attention/KV heads;
   - key/value dimensions;
   - RoPE;
   - sliding-window information;
   - MoE details.

3. **Tokenizer & capabilities**
   - tokenizer summary;
   - special tokens;
   - chat templates;
   - FIM capability;
   - recognized text/vision/audio/embedded draft capabilities.

4. **Memory & offload**
   - existing Phase 9 context control;
   - weight/KV/runtime-overhead estimates;
   - current fit vs total-hardware fit;
   - recommended GPU layers/device(s)/tensor split;
   - clear confidence and estimate labels.

5. **Tensor composition**
   - GGML type distribution;
   - parameter/encoded-byte distribution;
   - per-layer encoded sizes;
   - non-layer/unclassified totals.

6. **Raw metadata**
   - searchable key/type/value table;
   - lazy expansion/pagination for arrays and very long values;
   - copy key/value affordances where practical.

7. **Tensors**
   - searchable/paginated tensor table;
   - name, shape, type, elements, encoded bytes, shard, offset where available.

Do not place runtime status, Start/Stop, Always On, eviction controls or active-request information on this page.

The page may link to Instances that reference the Model, but runtime control stays on `/instances`.

## 15. Recommendation engine improvements

Refactor `internal/recommendations` to consume the shared normalized inspection.

### Weight/offload estimation

Prefer actual tensor encoded sizes and per-layer sizes over a uniform `file_size / block_count` assumption when available.

This allows recommendations to select the maximum exact sequence of offloadable layers that fits a device rather than treating every layer as equal-sized.

Retain conservative allowances for:

- runtime allocations;
- graph/work buffers;
- allocator fragmentation/safety margin;
- non-offloaded/non-layer tensors.

### KV-cache estimation

Use normalized architecture metadata including:

- block count;
- attention head count;
- KV head count;
- key/value dimensions;
- architecture-specific sliding-window behavior where supported;
- requested context length.

Where effective llama.cpp KV types are known from configuration, allow the estimator to use them. Otherwise label and use the current llama.cpp/default assumption explicitly.

Do not claim byte-exact runtime memory when llama.cpp runtime allocation details are still estimated.

### MoE

MoE metadata should improve explanations and derived compute characteristics, but weight residency must still account for all tensors required by llama.cpp. Do not size VRAM from only active experts unless runtime behavior explicitly supports that placement model.

## 16. Recommendation confidence

Confidence should be derived from the available evidence.

Example levels:

- **High** — complete local GGUF metadata plus tensor descriptors and current hardware snapshot;
- **Medium** — local metadata but some dimensions/tensor classification missing, or provider metadata with strong size/architecture information;
- **Low** — mostly filename/file-size heuristics or metadata parse warnings.

Expose the reason for reduced confidence rather than only a label.

## 17. Error and stale-file behavior

Inspection errors must be explicit and scoped.

Examples:

- invalid GGUF magic;
- unsupported GGUF version;
- truncated metadata;
- unreasonable length/count;
- missing split shard;
- artifact changed since cached inspection;
- unknown architecture key mapping.

Behavior:

- Model details remain accessible where possible;
- raw filesystem/model identity information still renders;
- recommendation confidence drops;
- Context capability stays manually editable;
- a previously explicit Context capability is not erased because re-scan failed;
- users can trigger/retry metadata refresh from the details page if a refresh action is implemented.

## 18. Security and resource limits

GGUF files are untrusted binary input.

The inspector must use defensive limits for:

- metadata count;
- tensor count;
- string lengths;
- array lengths;
- dimensions;
- integer multiplication/addition;
- per-request raw metadata page size;
- per-request tensor page size.

Never allocate an array merely because a GGUF header claims a huge element count.

Large tokenizer token/merge arrays must be represented lazily/bounded in APIs and UI.

## 19. Implementation slices

Implement in this order on `feature/phase-9-hardware-recommendations`:

### Slice 9A — Extract and harden GGUF parser

- create `internal/ggufmeta`;
- move generic binary reading from recommendations;
- preserve raw typed metadata;
- support bounded arrays;
- read tensor descriptors;
- add malformed/overflow/unknown-key tests.

### Slice 9B — Normalized metadata and split aggregation

- architecture-prefix resolver;
- Context capability;
- attention/KV/RoPE/SWA/MoE fields;
- tokenizer/chat/FIM/capability summary;
- parameter count;
- tensor-type composition;
- per-layer byte grouping;
- split-shard aggregation and warnings.

### Slice 9C — Model inspection/cache integration

- pre-save `/models/inspect` API;
- direct development-schema metadata cache/summary changes;
- cache fingerprint/invalidation;
- local Model add auto-fill;
- issue #25 behavior and tests.

### Slice 9D — Hugging Face post-download integration

- run the same inspector after complete downloads;
- populate registration defaults;
- refine provider recommendation confidence after local scan;
- split GGUF coverage.

### Slice 9E — Recommendation engine upgrade

- consume `ggufmeta.Inspection`;
- use exact/derived per-layer encoded sizes where available;
- improve KV calculations;
- preserve scheduler single-GPU-first placement behavior;
- add confidence reasons and estimate/fact separation.

### Slice 9F — Model details API/UI

- `GET /api/v1/models/:id/details`;
- bounded raw metadata endpoint;
- bounded tensor endpoint;
- `/models/:id/details` page;
- Details action on `/models`;
- Nuxt UI tabs/tables/search/alerts;
- context-sensitive memory/offload section.

### Slice 9G — Quality pass

- backend unit/integration tests;
- frontend Nuxt tests;
- split/corrupt/huge-array fixtures;
- authorization tests for management endpoints;
- 90% coverage gates;
- formatter/linter/type/build checks;
- spec consistency pass.

## 20. Testing requirements

Tests must not require a real GPU, llama-server process, external network, or multi-gigabyte model.

Build compact GGUF fixtures programmatically or store tiny deterministic fixtures covering at least:

- two different architecture prefixes;
- context detection;
- GQA/KV heads;
- explicit key/value dimensions;
- RoPE metadata;
- MoE metadata;
- chat template/FIM metadata;
- mixed tensor types;
- parameter count;
- per-layer tensor sizes;
- split GGUF aggregation;
- missing shard;
- unknown metadata key/type handling allowed by format;
- absent context metadata;
- malformed magic/version;
- truncated strings/arrays/tensor table;
- unreasonable lengths/counts;
- arithmetic overflow attempts;
- huge logical tokenizer arrays handled without eager allocation;
- metadata cache hit/stale invalidation;
- manual Context capability override preservation.

Frontend tests must cover:

- Details action/navigation;
- overview formatting;
- warnings/error state;
- raw metadata search and lazy array expansion;
- tensor pagination/search;
- context change updating recommendation output;
- no runtime lifecycle controls appearing on Model details.

## 21. Acceptance criteria

Phase 9 metadata expansion is complete when:

- [ ] GGUF parsing lives in a reusable package rather than in recommendations.
- [ ] Inspection never starts `llama-server` or reads tensor payloads merely to obtain metadata.
- [ ] Raw typed GGUF metadata is retained and accessible for every key.
- [ ] Huge arrays/values are exposed lazily/bounded instead of eagerly returned.
- [ ] Context capability uses `general.architecture` + the matching architecture-specific context key.
- [ ] `/models/new` auto-fills detected Context capability and keeps it editable.
- [ ] Automatic detection failure is non-blocking and never erases an explicit user value.
- [ ] Hugging Face-completed GGUFs use the same inspector before registration/defaulting.
- [ ] Architecture, layer count, embedding, attention/KV, RoPE/SWA and MoE data are normalized where present.
- [ ] Tokenizer/chat-template/FIM and recognized modality capabilities are summarized where present.
- [ ] Parameter count is derived from tensor shapes.
- [ ] Tensor/GGML type composition is available.
- [ ] Split GGUF parameter/tensor aggregation works without reading tensor payloads.
- [ ] Per-layer encoded sizes are available when tensor classification supports them.
- [ ] Recommendation logic consumes the shared inspection result.
- [ ] KV estimates use KV-head/key/value dimensions where available.
- [ ] Recommended offload uses per-layer sizes when available and remains single-GPU-first through scheduler placement policy.
- [ ] Exact metadata/tensor facts are visually distinguished from memory/fit estimates.
- [ ] `/models/:id/details` exists and is reachable from `/models` through a Details action.
- [ ] The details page shows normalized overview, architecture, tokenizer/capabilities, memory/offload, tensor composition, raw metadata and tensor descriptors.
- [ ] Raw metadata and tensor tables are searchable and bounded/paginated.
- [ ] Model details contain no Instance lifecycle/runtime controls.
- [ ] Invalid/corrupt/stale metadata produces a useful warning rather than crashing or blocking unrelated Model management.
- [ ] Backend and frontend coverage remain at or above the repository's 90% gates.
