# LlamaRack live compatibility suite

This directory contains the black-box conformance suite for issue #121. The suite targets a **running LlamaRack candidate** and deliberately invokes real OpenAI and LiteLLM clients instead of re-testing gateway internals with mocks.

## Contract

`contract.json` is the machine-readable 1.0 surface. `specs/016-inference-compatibility.md` is the normative human-readable contract.

The blocking suite pins exact client versions in `versions.json`. A separate non-blocking latest-client probe may override those pins, but release qualification never silently changes its known-good versions.

## Required target settings

All probes use these environment variables:

- `LLAMARACK_BASE_URL` — public OpenAI base URL including `/v1`, for example `http://127.0.0.1:8080/v1`.
- `LLAMARACK_API_KEY` — inference key used by the real clients.
- `LLAMARACK_CHAT_MODEL` — Instance ID used for baseline chat, streaming, error and disconnect checks.
- `LLAMARACK_RESPONSES_MODEL` — Responses-capable Instance ID; defaults to the chat model when omitted.
- `LLAMARACK_ARTIFACT_DIR` — evidence output directory; defaults to `artifacts/compat`.

Optional capability fixtures:

- `LLAMARACK_COMPLETION_MODEL`
- `LLAMARACK_EMBEDDING_MODEL`
- `LLAMARACK_TOOLS_MODEL`
- `LLAMARACK_STRUCTURED_MODEL`
- `LLAMARACK_VISION_MODEL`
- `LLAMARACK_TRANSCRIPTION_MODEL`

When a capability variable is absent the matching conditional row is reported `not_applicable`; it is never reported as passed. Set `LLAMARACK_REQUIRED_CAPABILITIES` to a comma-separated list to make missing fixtures fatal for a release candidate that advertises them.

## Lifecycle fixture settings

Lifecycle tests put existing Instances into a known state with the management API, then issue the actual compatibility request through `/v1`.

- `LLAMARACK_MANAGEMENT_BASE_URL` — manager origin without `/v1`, for example `http://127.0.0.1:8080`.
- `LLAMARACK_MANAGEMENT_KEY` — management/full API key accepted by `/api/v1/instances/*`.
- `LLAMARACK_LIFECYCLE_MODEL` — Instance ID safe for stop/start/autoload mutation during qualification.
- `LLAMARACK_FAILED_START_MODEL` — optional fixture intentionally configured to fail startup.

The lifecycle probe snapshots the Instance configuration before changing `autoload_enabled` and restores it before exit. The tested inference path itself remains `/v1`.

## LiteLLM Proxy settings

The direct LiteLLM client probe uses the same LlamaRack base URL and inference key. Proxy verification starts a local LiteLLM Proxy when requested by the runner and sends a client request through the proxy to LlamaRack.

The proxy config is generated at runtime so secrets are never committed. Evidence must redact bearer values.

## Evidence

Each probe writes JSON containing:

- target identifier/candidate digest supplied by the runner;
- pinned client and runtime versions;
- enabled fixture IDs/capabilities;
- per-case `pass`, `fail`, or `not_applicable` results;
- status/error summaries without credentials or prompt bodies.

Release qualification uploads the complete directory as a GitHub Actions artifact.

## Local execution

Use `scripts/release-qualification/compat.sh` after exporting the variables above. The script creates isolated Python/Node environments, installs the exact pinned client versions, runs Python SDK, Node SDK, raw-wire/lifecycle and LiteLLM probes, and writes a combined summary.

The normal PR CI only validates the contract and probe syntax. It does not require a real model, GPU, network service or external package registry during application unit tests.
