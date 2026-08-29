# Authentication and Management Security

## Status

This specification defines the v1 management-plane authentication model. It covers local accounts, OpenID Connect (OIDC), manager-issued bearer JWTs, revocable sessions, external identities and inference API keys.

SAML and RBAC are explicitly out of scope for v1.

## Security domains

llamacpp-manager has two independent credential domains:

- **Management plane** — the Nuxt UI and `/api/v1/*` use manager-issued JWT bearer credentials backed by server-side sessions.
- **Inference plane** — `/v1/*` uses inference API keys.

A management JWT MUST NOT authenticate inference requests. An inference API key MUST NOT authenticate management requests. OIDC provider tokens are never management API credentials; successful OIDC authentication always terminates in the same manager session/JWT model used by local login.

## Management JWTs

Protected management requests use:

```http
Authorization: Bearer <management-jwt>
```

JWT requirements:

- Ed25519 signatures;
- signing key persisted under the manager data directory and reused across restarts;
- one JWT per management session;
- no refresh-token pair;
- issuer `llamacpp-manager`;
- claims include `sub` (user ID), `sid` (server-side session ID), `iat`, `exp` and `jti`;
- no passwords, provider tokens, provider claims or other unnecessary personal information.

JWT verification is only the first authentication step. Every protected request MUST resolve the JWT's `sid`/`jti` against the authoritative session store and verify that the session has not expired and the user is still enabled. Session revocation therefore takes effect immediately even when the JWT's cryptographic expiry is later.

The server does not need to store plaintext management JWTs. The session record stores a one-way binding for the JWT identifier.

## Browser storage

Management JWTs are never stored in authentication cookies.

- **Remember me enabled:** `localStorage`.
- **Remember me disabled:** `sessionStorage`.
- Sign-out removes both possible stored copies.
- Startup restores the token from browser storage and validates it through `/api/v1/me` before considering the browser authenticated.

Management CSRF cookies/tokens are not used because management credentials are no longer ambient browser cookies. Origin checks remain where independently required, including local login/bootstrap and OIDC transaction protection.

## Server-side sessions

Every successful local or OIDC login creates a normal management session. Sessions remain authoritative and support:

- expiration;
- current-session logout;
- revoke one session;
- revoke all other sessions;
- revoke all sessions;
- invalidation when a user is disabled;
- password reset/change invalidation semantics;
- remote address, user agent, creation time and expiry in session listings.

## Bootstrap and local login

First-run bootstrap is local-account-only. No OIDC provider can create the initial management account and there is no default credential.

Local username/password login is enabled by default and configurable under **Administration → Authentication**. Local login may be disabled only if at least one OIDC provider is both enabled and has a persisted successful configuration test. Provider deletion, disabling or security-relevant edits MUST preserve the same lockout invariant while local login is disabled.

## OIDC providers

Multiple OIDC providers can be configured simultaneously. Each provider has a stable internal ID and includes:

- display name and enabled state;
- issuer;
- optional discovery URL;
- client ID;
- encrypted client secret;
- scopes;
- username claim;
- optional manual authorization, token and JWKS endpoints;
- last configuration-test state.

Standard discovery uses the issuer's `/.well-known/openid-configuration` endpoint unless an explicit discovery URL is configured. Manual endpoints may fill or replace discovered endpoints when required by the deployment.

Client secrets use the manager's existing encrypted provider-secret store. Plaintext is accepted only while creating/replacing a secret and is never returned by provider APIs. Provider responses expose only `secret_configured`.

### Provider testing

The Admin UI exposes **Test configuration**. Testing resolves discovery/manual endpoints, validates issuer consistency, checks the configured secret exists and verifies that the JWKS endpoint is reachable and contains keys. Testing does not provision a user or create a management session. A successful result is persisted and is used by the local-login lockout safeguard.

## OIDC browser flow

OIDC uses Authorization Code with state, nonce and PKCE S256.

1. Browser requests `/api/v1/auth/oidc/{provider}/start` with its Remember-me choice.
2. Manager creates a short-lived transaction containing state, nonce and PKCE verifier.
3. State is additionally bound to the initiating browser with a short-lived HttpOnly SameSite transaction cookie. This cookie is not a management credential.
4. Browser is redirected to the provider with state, nonce and PKCE challenge.
5. Provider returns to `/api/v1/auth/oidc/{provider}/callback`.
6. Manager consumes the browser transaction, exchanges the authorization code with the PKCE verifier, and validates the ID token signature, issuer, audience, expiry/time claims and nonce.
7. The external identity is resolved or provisioned and a normal manager session/JWT is created.
8. The JWT is **not** placed in the redirect URL. Manager creates a cryptographically random, very short-lived, single-use exchange code.
9. Browser returns to the configured frontend URL with only that exchange code. If no frontend URL is configured, the external URL is used for backward-compatible same-origin deployments.
10. Frontend POSTs the code to `/api/v1/auth/oidc/exchange`; successful consumption returns the manager JWT, expiry, user and Remember-me choice.

Exchange codes are deleted on first consumption and cannot be replayed.

## External and frontend URLs

OIDC callback generation uses the explicit `external_url` manager setting. Security-sensitive callback URLs are not inferred by blindly trusting arbitrary forwarded headers. The setting is intended for direct and reverse-proxy deployments, including local development with providers such as Authentik.

`frontend_url` is a separate optional manager setting used only as the browser destination after a successful OIDC callback. It defaults to an empty string. When empty, the final exchange-code redirect uses `external_url`, preserving the existing same-origin behavior. When set, the provider callback still targets the backend `external_url`, while the manager redirects the browser to `frontend_url` with the single-use `oidc_exchange` code after provider validation succeeds.

This separation allows development deployments such as a Nuxt frontend on `http://192.168.60.5:3000` with the manager API on `http://192.168.60.5:8888` without exposing the provider authorization code or provider tokens to the frontend.

## External identities

OIDC accounts are identified by immutable provider identity rather than usernames or email addresses. Stored identity data includes:

- provider ID;
- issuer;
- OIDC `sub`;
- linked management user ID;
- creation timestamp.

The `(provider, issuer, sub)` tuple is authoritative for subsequent login. A management user may link identities from multiple providers.

## JIT provisioning and linking

JIT provisioning is enabled by default and configurable globally.

When JIT is enabled and an unknown identity authenticates, username resolution uses the configured username claim and then sensible fallbacks such as `preferred_username`, `email`, `name`, and a deterministic subject-derived fallback.

Automatic matching to an existing management username is a separate setting and defaults to disabled. When disabled, an OIDC username collision fails safely and requires explicit linking; the manager does not silently create `alice2`-style names and does not take over the local account.

When JIT is disabled, unknown identities fail until an identity is explicitly pre-linked through management endpoints. Disabled users remain disabled regardless of successful provider authentication.

## WebSocket authentication

Native browser WebSockets do not expose a reliable standard way to set an `Authorization` header. Long-lived management JWTs therefore MUST NOT be placed in WebSocket URLs.

Flow:

1. Authenticated browser POSTs `/api/v1/auth/ws-ticket` with its Bearer JWT.
2. Manager verifies the JWT and authoritative session and issues a short-lived, single-use ticket bound to that session/JTI.
3. Browser connects to `/api/v1/ws?ticket=...`.
4. Server atomically consumes the ticket before upgrading the connection.
5. Reuse, expiry, revoked sessions and disabled users are rejected.

Runtime, log and observability streams continue over the authenticated WebSocket after upgrade.

## Logout

Initial logout is manager-local only. `POST /api/v1/auth/logout` revokes the current authoritative manager session. The frontend removes the stored JWT and closes runtime WebSockets. OIDC RP-initiated logout is not part of this phase.

## API surface

Public/pre-auth endpoints:

- `GET /api/v1/auth/bootstrap`
- `POST /api/v1/auth/bootstrap`
- `POST /api/v1/auth/login`
- `GET /api/v1/auth/providers`
- `GET /api/v1/auth/oidc/{provider}/start`
- `GET /api/v1/auth/oidc/{provider}/callback`
- `POST /api/v1/auth/oidc/exchange`

Bearer-protected endpoints include:

- `POST /api/v1/auth/logout`
- `POST /api/v1/auth/ws-ticket`
- `/api/v1/me` and session/password management
- `/api/v1/admin/auth/settings`
- `/api/v1/admin/auth/providers` and provider CRUD/test routes
- `/api/v1/admin/auth/identities` and unlink routes
- all other protected management APIs.

The v1 product intentionally remains flat-authorized: no roles, group mapping or provider-driven permission mapping are introduced by OIDC.