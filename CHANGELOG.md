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

- Official release candidates resolve the latest stable llama.cpp release at candidate-cut time and pin its immutable build for CPU and CUDA images.
- Stable LlamaRack releases are promoted from the already-qualified final RC runtime instead of silently selecting a newer llama.cpp build.
- Official container tags publish the exact SemVer tag `1.0.0` without a leading `v`, plus moving aliases `1.0`, `1`, and `latest` (`latest` is not SemVer), with corresponding `-cuda` variants.

## Release-entry convention

For each stable or prerelease, move relevant items out of **Unreleased** into a versioned section:

```text
## [1.0.0] - YYYY-MM-DD
```

Use the categories **Added**, **Changed**, **Deprecated**, **Removed**, **Fixed**, and **Security** when they apply. Release notes may be shorter or more narrative, but they must identify database/backup considerations, compatibility boundaries, the bundled llama.cpp release/build, and any upgrade action required from operators.
