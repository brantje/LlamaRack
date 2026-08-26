# 009 — Authentication, API Keys and Secret Storage

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines authentication for the llamacpp-manager web UI and management REST API, bearer API keys for the inference API, and secure provider-secret storage.

V1 uses local username/password authentication for the management plane and manager-generated bearer API keys for `/v1/*` inference access.

**Role-based access control is explicitly out of scope for v1.**

## 2. Goals

The security model must:

- require authenticated management access from day one;
- provide a secure first-user bootstrap flow;
- use one flat management permission level in v1;
- issue independent inference API keys;
- store only hashes of inference API keys;
- store provider secrets encrypted at rest;
- support API-key revocation/rotation;
- avoid exposing internal worker credentials or ports;
- provide safe session expiration/logout behavior;
- prevent unauthenticated direct API access even if the UI hides or exposes actions incorrectly.

## 3. Authentication domains

There are two intentionally separate credential domains.

### 3.1 Management authentication

Used by:

- Nuxt web UI;
- `/api/v1/*` management API.

Credential type:

- local username/password producing an authenticated session.

### 3.2 Inference authentication

Used by:

- `/v1/*` OpenAI-compatible API.

Credential type:

- manager-generated bearer API key.

A dashboard session is not an inference API key, and an inference API key does not grant management access.

## 4. V1 authorization model

V1 intentionally has **no RBAC, roles, permission matrix, custom roles, scopes or differentiated management capabilities**.

Rules:

- every authenticated management user has the same full management access;
- every unauthenticated management request is rejected, except the narrowly scoped bootstrap/login endpoints;
- the backend still enforces authentication server-side for every protected management endpoint;
- the frontend must not contain role-based navigation, role badges, capability gating or role-specific controls;
- user records do not need a role field for the v1 product contract;
- if an implementation retains a role column internally for forward compatibility, it must not affect v1 behavior or be exposed as a configurable v1 feature.

RBAC may be designed later without changing the separation between management sessions and inference API keys.

## 5. First-user bootstrap

On first start with no management users:

- manager enters bootstrap state;
- normal authenticated management operations are unavailable until the first user exists;
- UI shows a one-time setup flow to create the first management account;
- bootstrap creation is allowed only while the configured bootstrap condition is true, normally while no user exists;
- immediately after successful creation, the bootstrap endpoint becomes unavailable unless a documented recovery flow is explicitly invoked out of band.

Do not ship a default username/password.

If bootstrap is exposed over the network, the UI must clearly encourage initial setup before exposing the service broadly.

## 6. Password handling

Requirements:

- use a modern adaptive password hashing algorithm such as Argon2id or another current Go-supported equivalent chosen during implementation;
- store only salted password hashes;
- never log passwords;
- enforce a reasonable minimum password length;
- allow long passphrases;
- do not impose arbitrary composition rules such as mandatory punctuation unless required later;
- compare hashes using a timing-safe library implementation;
- rehash on successful login when password-hash parameters are upgraded.

## 7. Login

Login accepts username and password and creates a management session.

Security behavior:

- generic invalid-credentials message regardless of whether username exists;
- bounded/rate-limited repeated failures to reduce brute-force risk;
- disabled accounts cannot log in;
- successful login updates `last_login_at`;
- session identifier is unpredictable and protected as a secret;
- authentication cookies use appropriate HttpOnly/Secure/SameSite settings when cookie-based sessions are used.

## 8. Session model

Preferred v1 approach: server-side sessions referenced by an opaque secure cookie.

Advantages:

- immediate revocation;
- straightforward logout/all-sessions revocation;
- no long-lived self-contained management token;
- simple future extension if differentiated authorization is added after v1.

Requirements:

- session expiration;
- rolling/idle expiration may be used but must have a hard maximum if configured;
- logout invalidates the server-side session;
- password change can invalidate other sessions;
- disabling a user invalidates or blocks existing sessions promptly.

## 9. CSRF

If management authentication uses cookies, state-changing `/api/v1/*` operations require CSRF protection appropriate to the frontend architecture.

Options include:

- SameSite cookies plus an anti-CSRF token;
- another proven framework pattern.

Do not rely solely on the fact that the API returns JSON.

`/v1/*` bearer-key API is not cookie-authenticated and therefore uses a different CSRF threat model.

## 10. Management endpoint enforcement

Authentication is enforced in backend handlers/services, not only in frontend navigation.

Requirements:

- every protected management endpoint requires a valid session;
- unauthenticated protected requests return 401;
- authenticated users have full v1 management access and must not receive role/capability-based 403 responses;
- direct HTTP calls must not bypass authentication;
- bootstrap endpoints must stop working after initial setup.

A future RBAC implementation may add authorization checks, but those are not part of the v1 acceptance criteria.

## 11. User management

V1 may support multiple local management users, but all users are equivalent in permissions.

Authenticated management users may:

- list users;
- create users;
- enable/disable users;
- reset/change another user's password through an explicit secure workflow;
- remove users if product policy allows.

Recommended safeguards:

- prevent accidentally leaving the system with zero enabled management users unless a documented recovery path exists;
- require current-password reauthentication for changing one's own password or particularly sensitive actions if practical;
- clearly warn before disabling/deleting the current account.

Self-service password change is allowed for authenticated users.

There is no role selector or role-management API in v1.

## 12. Inference API keys

API keys are generated by the manager using cryptographically secure randomness.

Suggested display format can use a recognizable prefix such as `sk-lcm-`, but the prefix is a UX choice, not a security control.

Store:

- key ID;
- name;
- safe prefix/fingerprint;
- cryptographic hash of the full key;
- enabled/revoked state;
- creator;
- created time;
- last-used time.

Return plaintext only once immediately after creation/rotation.

Any authenticated management user may manage inference API keys in v1.

## 13. API key verification

For each `/v1/*` request:

1. parse Bearer token;
2. validate syntax/size;
3. find candidate by safe prefix/index strategy if used;
4. verify hash in timing-safe manner;
5. check enabled/revoked state;
6. update last-used metadata asynchronously/bounded so every token request does not create excessive SQLite contention;
7. proceed to model resolution only after success.

Do not log the token or full Authorization header.

## 14. API key lifecycle

Authenticated management users can:

- create;
- name/rename metadata;
- revoke/disable;
- rotate;
- delete historical metadata if desired.

Rotation should produce a new secret rather than attempt to reveal/recover the old one.

Immediate revocation is required for subsequent requests.

V1 does not require per-key model allowlists, rate limits, user ownership or permission scopes. Those can be added later by extending key policy.

## 15. Provider secrets

Initial provider secret:

- global Hugging Face access token.

Requirements:

- writable/deletable by authenticated management users in v1;
- encrypted before database storage;
- decrypted only inside the provider service when required;
- never returned after save;
- never embedded into frontend state;
- never included in logs, metrics, errors or crash reports;
- never forwarded to non-Hugging-Face download hosts.

The UI displays status such as `Configured` and optional safe metadata, not the secret.

## 16. Secret encryption key

The encryption-at-rest design requires a manager master key.

V1 should support a deployment-safe mechanism such as:

- externally supplied secret/file mounted into the container; or
- generated persistent key stored with restrictive permissions in the config directory.

Requirements:

- key must survive container restart when using the same persistent configuration volume;
- loss of key makes encrypted provider secrets unrecoverable but must not expose them;
- key is not stored in the same SQLite row as ciphertext as if that provided meaningful encryption;
- key never appears in UI/API.

## 17. Password reset/recovery

V1 does not have email-based recovery.

A documented local recovery path is required for self-hosted operation, for example a CLI/startup recovery command requiring direct access to the persistent config/container host.

Recovery must not become an unauthenticated HTTP endpoint available during normal operation.

## 18. Management API security

Baseline protections:

- authenticate every non-bootstrap protected endpoint;
- validate JSON/body sizes;
- use CSRF protection if cookie sessions are used;
- set secure headers appropriate to same-origin web app deployment;
- avoid returning stack traces/internal worker addresses;
- log security-relevant changes without recording secrets.

Management API and UI are expected to be same-origin in standard deployment.

## 19. Security events

A full immutable audit log is not a v1 requirement, but security-sensitive actions should at least produce structured application events/logs, including:

- login success/failure without passwords;
- user create/disable/password reset;
- API key create/revoke/rotate;
- provider token set/remove;
- destructive artifact deletion;
- global settings change.

If durable audit history is later required, these event points provide a foundation.

## 20. Sensitive diagnostics

Worker logs can contain upstream content or accidentally echo arguments/environment.

Therefore:

- redact known manager secrets before storage/display where feasible;
- never launch workers with provider tokens in command-line arguments;
- restrict full logs to authenticated management users;
- warn that logs may contain model/application content produced by upstream llama.cpp;
- manager should not intentionally log inference prompts by default.

## 21. Brute-force protection

Implement basic login protection using a bounded strategy such as:

- per-source and/or per-account attempt counters;
- exponential delay or temporary lockout;
- global safeguards to prevent memory abuse.

Avoid permanent lockout that requires database edits after trivial attacks.

Inference API keys are high-entropy and do not require the same password-style lockout, but repeated invalid keys may be rate-limited for abuse protection.

## 22. Network assumptions

The manager may be deployed on LAN or behind a reverse proxy.

Requirements:

- work correctly behind a reverse proxy when trusted-proxy settings are explicitly configured;
- do not automatically trust arbitrary `X-Forwarded-*` headers from the internet;
- allow TLS termination at a reverse proxy;
- mark cookies Secure when effective external scheme is HTTPS;
- document that exposing plain HTTP management login on an untrusted network is unsafe.

Built-in TLS is optional/not required for v1 if reverse-proxy deployment is documented.

## 23. CORS

Default deployment serves UI and API same-origin, so permissive CORS is unnecessary.

Recommended default:

- no broad cross-origin management access;
- `/v1` CORS should also be restrictive unless browser-based inference clients explicitly require configured origins;
- never default to credentialed wildcard origins.

A configurable allowed-origin list can be added if needed.

## 24. Error behavior

Management auth errors:

- unauthenticated: 401;
- invalid CSRF/session: safe 401/403 depending on framework semantics.

Inference auth errors use the OpenAI-compatible envelope defined in `006-openai-api.md`.

Errors must not reveal:

- password hashes;
- key hashes;
- provider tokens;
- whether a supplied invalid username exists during login;
- internal crypto keys.

## 25. Security-related configuration

Potential settings include:

- session lifetime;
- login protection thresholds;
- trusted reverse proxies;
- allowed origins if needed;
- secure-cookie/external URL behavior;
- direct-download LAN/SSRF policy from provider spec.

Do not expose low-level crypto parameters casually in UI unless there is a concrete operational use.

## 26. Testing requirements

Automated tests must cover:

- bootstrap only when no management user exists;
- valid/invalid login;
- disabled account behavior;
- session expiration and logout;
- CSRF rejection for protected cookie-auth mutations;
- all protected management endpoints reject unauthenticated requests;
- authenticated management users can perform all management operations without role checks;
- no role/capability-based behavior is required for v1;
- API key creation returns plaintext once;
- stored API-key record cannot recover plaintext;
- revoked API key immediately fails;
- invalid inference key cannot trigger autoload;
- Hugging Face token API never returns stored plaintext;
- secret encryption/decryption across manager restart with persistent master key;
- logs redact known credential patterns.

## 27. Invariants

1. No default management password exists.
2. Passwords are never stored reversibly.
3. Inference API key plaintext is not stored.
4. Management sessions and inference API keys are separate credential domains.
5. Every protected management operation requires authentication server-side.
6. Invalid inference authentication cannot cause model startup/resource consumption.
7. Provider tokens are encrypted at rest and never returned after save.
8. V1 has no RBAC or differentiated management permissions.
9. Bootstrap HTTP functionality is disabled after initial account creation.
10. Secret values never appear in metrics.

## 28. Deferred authorization scope

Explicitly deferred until after v1:

- Admin / Operator / Read-only roles;
- custom roles;
- per-endpoint capability matrices;
- role-specific frontend controls;
- per-user model permissions;
- per-key permission scopes or model allowlists.

Adding RBAC later requires a separate design/update to this specification and the data/UI contracts.

## 29. Acceptance criteria

Authentication/security is complete for v1 when:

- first-run setup creates the first management user without a default credential;
- subsequent management access requires login;
- every authenticated management user has the same full management access;
- sessions can be revoked and expire;
- inference clients authenticate with independent generated keys;
- key revocation works without manager restart;
- Hugging Face token survives restart encrypted and is usable by the provider while remaining unreadable through management APIs;
- direct unauthenticated calls receive correct 401 results;
- a lost-access scenario has a documented local recovery path;
- security-sensitive logs contain safe metadata but no plaintext credentials;
- no RBAC implementation or role-specific UI is required for v1.