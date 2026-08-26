# 008 — Model Providers and Downloads

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how llamacpp-manager discovers, evaluates, downloads and stores model artifacts from external sources.

V1 supports:

- Hugging Face as a first-class searchable provider;
- direct HTTP/HTTPS URLs;
- GGUF artifacts, including split GGUF sets;
- one local model storage directory;
- one global Hugging Face token for authenticated/private/gated access.

## 2. Goals

The provider/download subsystem must:

- present a useful Hugging Face model browser;
- identify GGUF files and group quantizations;
- identify split GGUF shards as one logical artifact;
- provide hardware-aware fit/recommendation information;
- download large files reliably;
- resume transfers where the source supports HTTP range requests;
- expose progress, speed, state and failures;
- never expose a partial download as a completed artifact;
- support authenticated Hugging Face requests;
- store files safely under one configured models directory;
- keep provider-specific behavior behind a provider abstraction so additional sources can be added later.

## 3. Non-goals for v1

- ModelScope or other searchable registries;
- multiple Hugging Face accounts/tokens;
- arbitrary Git repositories;
- automatic filesystem watching/import;
- storage pools or tiering;
- automatic deletion based on disk pressure;
- peer-to-peer distribution;
- background model conversion/quantization;
- automatic license acceptance on behalf of the user.

## 4. Provider abstraction

Provider-specific code should expose normalized capabilities conceptually like:

- search models;
- fetch model/repository details;
- list candidate files;
- resolve downloadable file URLs/metadata;
- provide authentication headers internally;
- expose source revision/identity;
- indicate resumability/size/checksum where available.

Not every provider needs to implement search. Direct URL is a valid provider with only artifact-resolution/download capability.

The download manager consumes normalized source objects rather than embedding Hugging Face API logic throughout the application.

## 5. Hugging Face search

The UI should support server-mediated search with at least:

- free-text query;
- sorting by useful provider-supported fields such as popularity/downloads/likes/recent update where available;
- GGUF-focused filtering;
- author/organization filter where practical;
- architecture/model metadata filters where provided;
- pagination/infinite loading.

The backend should call Hugging Face APIs directly. The browser must not require the user to paste repository URLs for normal use.

Provider response data is untrusted external metadata and must be normalized/escaped before display.

## 6. Hardware-aware discovery

Search/result/detail views should use local hardware information to improve usefulness without excluding choices.

For each candidate GGUF quantization, the manager may show:

- file/download size;
- estimated runtime RAM;
- estimated VRAM for likely offload settings;
- whether it is likely to fit currently available hardware;
- whether it is likely to fit total hardware after eligible model unloading;
- estimated confidence;
- a recommended choice.

Recommendations are advisory. The user may choose any compatible artifact even if it does not fit current hardware.

## 7. Model detail view

Selecting a Hugging Face result should fetch enough repository information to show:

- repository ID;
- author/organization;
- description/model card summary where practical;
- last update;
- relevant tags/architecture metadata;
- GGUF files grouped by quantization/logical artifact;
- file sizes;
- split shard count;
- gated/private/auth requirement state where detectable;
- local download status if this exact artifact/revision is already known.

Avoid rendering arbitrary model-card HTML unsafely. Sanitized Markdown/plain text is preferred.

## 8. GGUF identification

A file is a GGUF candidate when provider metadata/path indicates a `.gguf` artifact.

The manager should parse filenames and, when practical, GGUF metadata to identify:

- model/variant name;
- quantization such as Q4_K_M, Q5_K_M, Q8_0, etc.;
- shard naming;
- architecture/metadata after download or via remote metadata when possible.

Filename parsing is heuristic. Do not treat an unrecognized filename as invalid solely because its quantization cannot be parsed.

## 9. Split GGUF grouping

Recognize conventional shard patterns such as:

```text
name-00001-of-00004.gguf
name-00002-of-00004.gguf
name-00003-of-00004.gguf
name-00004-of-00004.gguf
```

The provider layer groups these into one logical download candidate.

Requirements:

- selecting any grouped artifact downloads all required shards;
- verify expected shard count before marking artifact complete;
- retain shard ordering/index metadata;
- show one logical item in the model library;
- calculate total size across shards;
- if one shard fails, the artifact remains incomplete while successful shards may be retained for resume.

Do not merge shards into one file unless llama.cpp explicitly requires that; preserve the upstream format.

## 10. Quantization grouping

Repository file lists may contain many variants. The model detail UI should group candidate artifacts by detected quantization and variant.

A logical option includes:

- display quantization;
- constituent file(s);
- total size;
- recommendation status;
- estimated memory needs;
- local state.

If multiple unrelated files share a quantization label, keep enough filename/variant identity to avoid combining them incorrectly.

## 11. Quantization explanations

The application should maintain curated explanatory metadata for common quantization families.

The UI may explain relative concepts such as:

- smaller/larger memory footprint;
- approximate quality tradeoff;
- likely performance implications;
- common balanced recommendations.

Do not present universal benchmark numbers as facts because speed/quality varies by model and hardware.

Recommendations should say why an option is selected, e.g. “largest quantization estimated to fit selected GPU memory with safety margin.”

## 12. Recommendation engine inputs

Use available inputs such as:

- artifact size;
- parsed/known quantization;
- parameter count/architecture;
- system RAM;
- GPU total/free memory;
- configured safety margins;
- context-size assumption/default;
- likely KV cache requirements;
- available GPU offload capability.

Recommendation output should include a confidence/quality indicator because pre-download metadata may be incomplete.

After download and GGUF metadata parsing, estimates can be refined.

## 13. Hugging Face authentication

V1 supports one global Hugging Face token stored as an encrypted application secret.

Requirements:

- token can be added/replaced/removed by Admin only;
- plaintext is accepted only over authenticated management API/UI;
- API never returns the token after storage;
- UI shows configured/not-configured and optionally a safe token prefix if appropriate;
- token is sent only to Hugging Face endpoints that require/provider logic authorizes;
- Authorization headers are never logged.

The token may allow access to private repositories and gated repositories for which the user's Hugging Face account already has permission.

The manager must not attempt to bypass provider gating or automatically agree to licenses/access terms.

## 14. Direct URL provider

The direct URL workflow accepts an HTTP or HTTPS URL to a GGUF file or, where explicitly supported, a known split file set.

Initial behavior:

1. validate URL scheme;
2. perform safe metadata request where possible;
3. determine filename from headers/path or require/allow user override;
4. detect size/range support;
5. create download job;
6. download to a temporary file under the model directory;
7. validate completion;
8. atomically promote to final artifact.

Direct URL does not imply arbitrary server-side fetch of every scheme/host without security controls.

## 15. SSRF and URL safety

Direct URL downloads create SSRF risk.

At minimum:

- allow only HTTP/HTTPS;
- reject malformed URLs and embedded credentials;
- resolve redirects carefully;
- by default reject loopback, link-local, multicast and private-network destinations unless an explicit administrator setting allows LAN downloads;
- re-check redirect targets against the same policy;
- enforce reasonable redirect count;
- do not forward Hugging Face credentials to arbitrary hosts.

Because home-lab users may intentionally host models on LAN, private-network downloads can be an explicit Admin opt-in rather than permanently impossible.

## 16. Download job states

Canonical states:

- `QUEUED`
- `RESOLVING`
- `DOWNLOADING`
- `PAUSED` if pause is implemented safely;
- `VERIFYING`
- `COMPLETED`
- `FAILED`
- `CANCELLED`

A logical split download may have per-file substates while exposing one aggregate job state.

## 17. Temporary files and atomic completion

Each download writes to a temporary destination, for example a `.part` file or manager-controlled temporary filename.

Rules:

- temporary files are never treated as model artifacts ready for loading;
- final filenames appear only after expected byte count/checksum validation as available;
- promotion to final path is atomic where filesystem semantics allow;
- interrupted jobs can discover their own partial state after restart;
- unrelated `.part` files are not automatically trusted/imported.

## 18. Resume behavior

When a source supports byte ranges and a matching partial file exists:

- validate source identity/ETag/Last-Modified/checksum metadata where available;
- resume at the correct byte offset;
- if source identity changed, restart cleanly rather than append incompatible data;
- persist enough job metadata to resume after manager restart.

If range requests are unsupported, retry restarts the affected file from zero.

For split GGUF, completed shards do not need to be downloaded again if their identity/size validation still succeeds.

## 19. Retry policy

Retry transient failures such as temporary network disconnects or retryable 5xx responses with bounded exponential backoff.

Do not endlessly retry:

- 401/403 authentication/access failures;
- 404 missing files;
- permanent invalid URL responses;
- checksum mismatch beyond a bounded retry count.

Expose retry count and last failure to the UI.

## 20. Cancellation

Users with sufficient permission can cancel a download.

Cancellation:

- stops network transfer promptly;
- marks job CANCELLED;
- may keep partial data to support explicit future resume/retry unless product settings choose cleanup;
- never promotes partial files;
- is idempotent.

The UI should distinguish Cancel from Delete partial data if both operations exist.

## 21. Disk-space checks

Before starting, estimate required disk capacity using known total download size plus safety margin.

If total size is unknown, display that uncertainty and enforce a minimum free-space policy where practical.

During download, disk-full errors become explicit failures without corrupting already-completed artifacts.

V1 uses one configured model storage directory and reports its capacity/free space in Discover/Downloads/Settings.

## 22. Filename and path safety

Provider filenames must be sanitized as filenames, not trusted paths.

Requirements:

- strip/reject directory traversal;
- prevent absolute path escapes;
- prevent collisions through deterministic conflict handling;
- ensure every final/temporary destination resolves under the configured models directory;
- retain provider original filename as metadata if sanitization changes it.

## 23. Artifact identity and duplicates

Avoid downloading the exact same provider artifact repeatedly by matching available identity such as:

- provider;
- repository/source;
- revision/commit if known;
- filename/logical shard set;
- checksum/ETag where known.

If a matching completed local artifact exists, offer to use it rather than silently duplicate it.

Different revisions or checksums are distinct artifacts even if filenames match.

## 24. Post-download processing

After all files complete:

1. verify size/checksum where available;
2. validate expected split shard completeness;
3. inspect GGUF metadata where supported;
4. refine quantization/architecture/resource metadata;
5. persist completed artifact;
6. optionally offer/create a Model configuration in the next UI step.

A download does not automatically need to create a public model ID without user confirmation/configuration.

## 25. Model deletion versus artifact deletion

Provider/download state follows the data-model separation:

- deleting a Model does not necessarily remove the downloaded artifact;
- deleting an artifact requires no live model reference or an explicit workflow that resolves dependents;
- deleting artifact files stops any running model first through lifecycle if a destructive dependent workflow is selected.

## 26. Provider caching

Search/detail results may be cached for a bounded duration to reduce provider traffic.

Caching requirements:

- authentication context must be respected;
- private metadata must not be exposed to unauthorized management users;
- stale cache should not claim a download URL is valid indefinitely;
- actual download resolution may refresh signed/temporary URLs when needed.

## 27. Rate limits and provider failures

Provider APIs may throttle requests.

The backend should:

- respect Retry-After where available;
- classify rate limit separately from generic network failure;
- avoid making one provider search request per frontend keystroke without debounce/cache;
- surface authentication/rate-limit/provider outage messages clearly.

## 28. Download metrics

Expose bounded metrics such as:

- active jobs;
- bytes downloaded;
- download failures by provider/reason class;
- duration;
- current aggregate throughput;
- completed artifact count.

Do not place full URLs, repository IDs with unbounded cardinality or tokens into Prometheus labels unless carefully bounded; detailed identifiers belong in logs/UI state.

## 29. UI requirements summary

Discover must support:

- search/filter/sort;
- hardware fit badges;
- recommended quantization;
- model details;
- one-click selection of a logical GGUF artifact;
- gated/private error states;
- current local/download state.

Downloads must support:

- active/history list;
- progress bar;
- bytes/total;
- speed;
- ETA when computable;
- state;
- retry;
- cancel;
- error details.

## 30. Invariants

1. Partial downloads are never loadable completed artifacts.
2. Split GGUF is one logical artifact with all expected shards.
3. Provider filenames cannot escape the models directory.
4. Hugging Face tokens are never sent to direct URL hosts.
5. Gated/private access is used only with legitimate provider authentication.
6. Recommendation does not prevent manual artifact choice.
7. Completed artifact identity includes enough source/revision data to detect duplicates safely.
8. Resume never appends to a changed/incompatible remote object.
9. Download cancellation is idempotent.
10. Model definition deletion does not silently delete multi-gigabyte artifacts.

## 31. Acceptance criteria

Before v1, tests must demonstrate:

- Hugging Face search returns normalized results to the UI;
- a repository with multiple GGUF quantizations is grouped sensibly;
- a four-shard GGUF selection creates one logical artifact and downloads all four shards;
- hardware fit/recommendation can rank candidate quantizations without hiding alternatives;
- global Hugging Face token enables authorized private/gated metadata/download access and is never returned through the API;
- direct URL download supports a normal single GGUF;
- resumable HTTP range download resumes a known partial file;
- changed remote identity causes safe restart instead of corrupt append;
- cancellation never promotes a partial file;
- disk-full/network/checksum failures remain FAILED with recoverable diagnostics;
- path traversal filenames are rejected/sanitized safely;
- completed download is parsed into artifact/GGUF metadata;
- an already-downloaded matching artifact is detected rather than silently duplicated.