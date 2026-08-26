# 009 — Authentication, API Keys and RBAC

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines authentication and authorization for the llamacpp-manager web UI, management REST API, inference API, and provider secrets.

V1 uses local username/password authentication for the management plane and manager-generated bearer API keys for `/v1/*` inference access.

## 2. Goals

The security model must:

- require authenticated management access from day one;
- provide a secure bootstrap-admin flow;
- support Admin, Operator and Read-only roles;
- use server-side authorization for every management mutation/read requiring protection;
- issue independent inference API keys;
- store only hashes of inference API keys;
- store provider secrets encrypted at rest;
- support revocation/rotation;
- avoid exposing internal worker credentials or ports;
- provide safe session expiration/logout behavior;
- prevent authorization bypass through direct API calls even if the UI hides actions.

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

A dashboard session token is not an inference API key, and an inference API key does not grant management access.

## 4. Bootstrap administrator

On first start with no users:

- manager enters bootstrap state;
- normal authenticated management operations are unavailable until an Admin exists;
- UI shows a one-time setup flow to create the first administrator;
- bootstrap creation is allowed only while the user table has no valid administrator/user according to the chosen bootstrap condition;
- immediately after successful creation, bootstrap endpoint becomes permanently unavailable unless a documented recovery flow is explicitly invoked out of band.

Do not ship a default username/password.

If bootstrap is exposed over the network, the UI must clearly encourage initial setup before exposing the service broadly.

## 5. Password handling

Requirements:

- use a modern adaptive password hashing algorithm such as Argon2id or another current Go-supported equivalent chosen during implementation;
- store only salted password hashes;
- never log passwords;
- enforce a reasonable minimum password length;
- allow long passphrases;
- do not impose arbitrary composition rules such as mandatory punctuation unless required later;
- compare hashes in a timing-safe library implementation;
- rehash on successful login when password-hash parameters are upgraded.

## 6. Login

Login accepts username and password and creates a management session.

Security behavior:

- generic invalid-credentials message regardless of whether username exists;
- bounded/rate-limited repeated failures to reduce brute-force risk;
- disabled accounts cannot log in;
- successful login updates `last_login_at`;
- session identifier is unpredictable and protected as a secret;
- authentication cookies use appropriate HttpOnly/Secure/SameSite settings when cookie-based sessions are used.

## 7. Session model

Preferred v1 approach: server-side sessions referenced by an opaque secure cookie.

Advantages:

- immediate revocation;
- simple role-change behavior;
- no long-lived self-contained token containing stale authorization claims;
- straightforward logout/all-sessions revocation.

Requirements:

- session expiration;
- rolling/idle expiration may be used but must have a hard maximum if configured;
- logout invalidates server-side session;
- password change can invalidate other sessions;
- disabling a user invalidates or blocks existing sessions promptly;
- role changes take effect without waiting for a long token lifetime.

## 8. CSRF

If management authentication uses cookies, state-changing `/api/v1/*` operations require CSRF protection appropriate to the frontend architecture.

Options include:

- SameSite cookies plus an anti-CSRF token;
- another proven framework pattern.

Do not rely solely on the fact that the API returns JSON.

`/v1/*` bearer-key API is not cookie-authenticated and therefore uses a different CSRF threat model.

## 9. Roles

V1 roles:

- `admin`
- `operator`
- `readonly`

Roles are intentionally coarse. Fine-grained custom roles are out of scope.

## 10. Authorization matrix

Baseline permissions:

| Capability | Admin | Operator | Read-only |
|---|---:|---:|---:|
| View dashboard/health | Yes | Yes | Yes |
| View models/config | Yes | Yes | Yes |
| View instance status/logs | Yes | Yes | Yes |
| View download status | Yes | Yes | Yes |
| Start/stop/restart models | Yes | Yes | No |
| Create/edit model config | Yes | Yes | No |
| Create instance definitions / GPU assignment | Yes | Yes | No |
| Start/cancel/retry downloads | Yes | Yes | No |
| Discover/browse provider models | Yes | Yes | Yes |
| Delete model definitions | Yes | No | No |
| Delete local artifacts/files | Yes | No | No |
| Manage users/roles | Yes | No | No |
| Manage inference API keys | Yes | No | No |
| Manage Hugging Face token/secrets | Yes | No | No |
| Change global/system settings | Yes | No | No |
| Change global llama.cpp defaults | Yes | No | No |
| View sensitive diagnostics | Yes | limited | limited |

Implementation may refine individual read-only diagnostic fields, but must not broaden Operator into security administration implicitly.

## 11. Authorization enforcement

Authorization is enforced in backend handlers/services, not only in frontend navigation.

Every protected endpoint declares its required capability/role.

The frontend consumes user role/capability data to hide/disable impossible actions for UX, but direct HTTP calls must still be rejected server-side.

Authorization failures should return 403 for authenticated users lacking permission.

## 12. User management

Admin can:

- list users;
- create users;
- assign role;
- enable/disable users;
- reset/change another user's password through an explicit secure workflow;
- remove users if product policy allows.

Recommended safeguards:

- prevent an administrator from accidentally leaving the system with zero enabled Admin users;
- require current-password reauthentication for changing one's own password or particularly sensitive actions if practical;
- clearly warn before disabling/deleting the current account.

Self-service password change is allowed for authenticated users.

## 13. Inference API keys

API keys are generated by the manager using cryptographically secure randomness.

Suggested display format can use a recognizable prefix such as `sk-lcm-`, but prefix is a UX choice, not a security control.

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

## 14. API key verification

For each `/v1/*` request:

1. parse Bearer token;
2. validate syntax/size;
3. find candidate by safe prefix/index strategy if used;
4. verify hash in timing-safe manner;
5. check enabled/revoked state;
6. update last-used metadata asynchronously/bounded so every token request does not create excessive SQLite contention;
7. proceed to model resolution only after success.

Do not log the token or full Authorization header.

## 15. API key lifecycle

Admin can:

- create;
- name/rename metadata;
- revoke/disable;
- rotate;
- delete historical metadata if desired.

Rotation should produce a new secret rather than attempt to reveal/recover the old one.

Immediate revocation is required for subsequent requests.

V1 does not require per-key model allowlists, rate limits or user ownership. Those can be added later by extending key policy.

## 16. Provider secrets

Initial provider secret:

- global Hugging Face access token.

Requirements:

- Admin-only write/delete;
- encrypted before database storage;
- decrypted only inside provider service when required;
- never returned after save;
- never embedded into frontend state;
- never included in logs, metrics, errors or crash reports;
- never forwarded to non-Hugging-Face download hosts.

The UI displays status such as `Configured` and optional safe metadata, not the secret.

## 17. Secret encryption key

The encryption-at-rest design requires a manager master key.

V1 should support a deployment-safe mechanism such as:

- externally supplied secret/file mounted into the container; or
- generated persistent key stored with restrictive permissions in the config directory.

The exact implementation may choose one default and support another, but requirements are:

- key must survive container restart when using the same persistent configuration volume;
- loss of key makes encrypted provider secrets unrecoverable but must not expose them;
- key is not stored in the same SQLite row as ciphertext as if that provided meaningful encryption;
- key never appears in UI/API.

## 18. Password reset/recovery

V1 does not have email-based recovery.

A documented local administrator recovery path is required for self-hosted operation, for example a CLI/startup recovery command requiring direct access to the persistent config/container host.

Recovery must not become an unauthenticated HTTP endpoint available during normal operation.

Exact recovery command is implementation work, but the architecture must reserve a safe out-of-band method.

## 19. Management API security

Baseline protections:

- authenticate every non-bootstrap protected endpoint;
- authorize each operation;
- validate JSON/body sizes;
- use CSRF protection if cookie sessions are used;
- set secure headers appropriate to same-origin web app deployment;
- avoid returning stack traces/internal worker addresses;
- log security-relevant changes without recording secrets.

Management API and UI are expected to be same-origin in standard deployment.

## 20. Audit/security events

A full immutable audit log is not a v1 requirement, but security-sensitive actions should at least produce structured application events/logs, including:

- login success/failure (without passwords);
- user create/disable/role change;
- API key create/revoke/rotate;
- provider token set/remove;
- destructive artifact deletion;
- global settings change.

If durable audit history is later required, these event points provide a foundation.

## 21. Sensitive diagnostics

Worker logs can contain upstream content or accidentally echo arguments/environment.

Therefore:

- redact known manager secrets before storage/display where feasible;
- never launch workers with provider tokens in command-line arguments;
- restrict full logs to authenticated users;
- Read-only may view logs because the selected RBAC model permits operational read access, but the UI should warn logs may contain model/application content produced by upstream llama.cpp;
- manager should not intentionally log inference prompts by default.

## 22. Brute-force protection

Implement basic login protection using a bounded strategy such as:

- per-source and/or per-account attempt counters;
- exponential delay or temporary lockout;
- global safeguards to prevent memory abuse.

Avoid permanent lockout that requires database edits after trivial attacks.

Inference API keys are high-entropy and do not require the same password-style lockout, but repeated invalid keys may be rate-limited for abuse protection.

## 23. Network assumptions

The manager may be deployed on LAN or behind a reverse proxy.

Requirements:

- work correctly behind a reverse proxy when trusted-proxy settings are explicitly configured;
- do not automatically trust arbitrary `X-Forwarded-*` headers from the internet;
- allow TLS termination at a reverse proxy;
- mark cookies Secure when effective external scheme is HTTPS;
- document that exposing plain HTTP management login on an untrusted network is unsafe.

Built-in TLS is optional/not required for v1 if reverse-proxy deployment is documented.

## 24. CORS

Default deployment serves UI and API same-origin, so permissive CORS is unnecessary.

Recommended default:

- no broad cross-origin management access;
- `/v1` CORS should also be restrictive unless browser-based inference clients explicitly require configured origins;
- never default to credentialed wildcard origins.

A configurable allowed-origin list can be added if needed.

## 25. Error behavior

Management auth errors:

- unauthenticated: 401;
- authenticated but unauthorized: 403;
- invalid CSRF/session: safe 401/403 depending on framework semantics.

Inference auth errors use the OpenAI-compatible envelope defined in `006-openai-api.md`.

Errors must not reveal:

- password hashes;
- key hashes;
- provider tokens;
- whether a supplied invalid username exists during login;
- internal crypto keys.

## 26. Security-related configuration

Potential settings include:

- session lifetime;
- login protection thresholds;
- trusted reverse proxies;
- allowed origins if needed;
- secure-cookie/external URL behavior;
- direct-download LAN/SSRF policy from provider spec.

Do not expose low-level crypto parameters casually in UI unless there is a concrete operational use.

## 27. Testing requirements

Automated tests must cover:

- bootstrap only when no admin exists;
- valid/invalid login;
- disabled account behavior;
- session expiration and logout;
- CSRF rejection for protected cookie-auth mutations;
- every RBAC boundary in the authorization matrix;
- prevention of last-admin removal/disable where policy requires;
- API key creation returns plaintext once;
- stored record cannot recover plaintext;
- revoked API key immediately fails;
- invalid inference key cannot trigger autoload;
- Hugging Face token API never returns stored plaintext;
- secret encryption/decryption across manager restart with persistent master key;
- logs redact known credential patterns;
- non-Admin cannot change users, API keys, global settings or provider token.

## 28. Invariants

1. No default management password exists.
2. Passwords are never stored reversibly.
3. Inference API key plaintext is not stored.
4. Management sessions and inference API keys are separate credential domains.
5. Authorization is enforced server-side.
6. Invalid inference authentication cannot cause model startup/resource consumption.
7. Provider tokens are encrypted at rest and never returned after save.
8. Admin-only operations cannot be reached by Operator/Read-only through direct API calls.
9. Bootstrap HTTP functionality is disabled after initial admin creation.
10. Secret values never appear in metrics.

## 29. Acceptance criteria

Security/RBAC is complete for v1 when:

- first-run setup creates an Admin without a default credential;
- subsequent access requires login;
- Admin, Operator and Read-only behavior matches the matrix;
- sessions can be revoked/expire and role changes take effect promptly;
- inference clients authenticate with independent generated keys;
- key revocation works without manager restart;
- Hugging Face token survives restart encrypted and is usable by the provider while remaining unreadable through management APIs;
- direct unauthorized calls receive correct 401/403 results;
- a lost-admin scenario has a documented local recovery path;
- security-sensitive logs contain safe metadata but no plaintext credentials.