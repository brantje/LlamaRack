# Security Policy

## Supported versions

LlamaRack follows semantic versioning for stable releases.

| Version | Security support |
| --- | --- |
| Latest `1.x` stable minor | Supported |
| Older `1.x` minors | Best effort until users can upgrade safely; no long-term-support commitment |
| Prerelease / development builds | Not supported as stable releases; reports are still useful |

Security fixes may be released as compatible patch releases and may require upgrading to the newest supported `1.x` minor. The project does not currently promise an LTS branch beyond the documented `1.x` compatibility contract.

## Reporting a vulnerability

Please do not open a public issue for a vulnerability that could put users at risk.

Use GitHub's private vulnerability reporting / Security Advisory flow for this repository. In GitHub, open the repository's **Security** tab and choose **Report a vulnerability**. If that option is unavailable, contact a repository maintainer privately before publishing technical details.

A useful report includes:

- the affected LlamaRack version and Git commit;
- the bundled llama.cpp release/build and runtime variant shown under **Administration → System**;
- deployment details relevant to the issue;
- a clear description of impact and prerequisites;
- reproduction steps or a minimal proof of concept when safe to provide;
- logs or request identifiers with credentials and other secrets removed;
- whether the issue is already known or publicly disclosed elsewhere.

## Handling and disclosure

Maintainers will try to acknowledge private reports promptly, reproduce and assess impact, coordinate a fix when one is needed, and agree on a reasonable disclosure timeline with the reporter. Exact response or release times are not guaranteed.

Please give maintainers a reasonable opportunity to investigate before public disclosure. Once a fix is available, the project may publish a GitHub Security Advisory and release notes describing affected versions, mitigations, and the fixed release without exposing unnecessary exploit detail.

Security reports do not override the normal compatibility policy: a security fix should remain backward compatible within `1.x` where practical, but protecting users can require narrowly scoped behavior changes when there is no safe compatible alternative.
