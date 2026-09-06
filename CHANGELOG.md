# Changelog

LlamaRack uses this file for durable, user-facing release history. Entries describe supported product behavior, compatibility changes, migrations, security fixes, and operationally relevant changes rather than reproducing the Git commit log.

The format is based on Keep a Changelog and releases follow Semantic Versioning.

## [Unreleased]

### Added

- Canonical build identity containing the LlamaRack version, Git commit, runtime variant, and bundled llama.cpp release/build.
- Build identity in Administration → System and `/api/v1/system` diagnostics.
- Reproducible release-candidate runtime selection and stable promotion policy.
- Explicit `1.x` compatibility, release, security, and licensing policies.

### Changed

- Official release candidates pin the current published llama.cpp GHCR server images by digest and record that build's identity. When those images match GitHub `releases/latest`, the GitHub stable release name is recorded; otherwise the published GHCR `bNNNN` is recorded instead of waiting for unpublished `server-bNNNN` tags.
- Stable LlamaRack releases are promoted from the already-qualified final RC runtime instead of silently selecting a newer llama.cpp build.
- Official container tags publish the exact SemVer tag `1.0.0` without a leading `v`, plus moving aliases `1.0`, `1`, and `latest` (`latest` is not SemVer), with corresponding `-cuda` variants.

### Security

- Startup now enforces private Unix modes on the configuration directory (`0700`) and SQLite database plus WAL/SHM sidecars (`0600`), including repair of existing overly permissive files, and fails clearly when the filesystem cannot honor `chmod` ([#135](https://github.com/brantje/LlamaRack/issues/135)).
- Authenticated inference keys can no longer coerce `/v1/slots/{slot_id}` into private llama.cpp paths outside `/slots/{id}` ([#139](https://github.com/brantje/LlamaRack/issues/139)).

### Fixed

- Startup no longer treats a world-writable Docker bind-mounted config directory (`0777`, no sticky bit) as a shared path like `/tmp`; those dedicated volumes are tightened to `0700` as intended ([#135](https://github.com/brantje/LlamaRack/issues/135)).
- Release-container runtime resolution no longer spends a 10-minute retry budget on llama.cpp GitHub stable tags that GHCR has not published.

## Release-entry convention

For each stable or prerelease, move relevant items out of **Unreleased** into a versioned section:

```text
## [1.0.0] - YYYY-MM-DD
```

Use the categories **Added**, **Changed**, **Deprecated**, **Removed**, **Fixed**, and **Security** when they apply. Release notes may be shorter or more narrative, but they must identify database/backup considerations, compatibility boundaries, the bundled llama.cpp release/build, and any upgrade action required from operators.
