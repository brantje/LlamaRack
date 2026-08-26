# AGENTS.md

## Quality gates

Test coverage is a hard repository rule.

- Every change MUST keep automated test coverage at **90.0% or higher** for first-party, testable application code.
- New features and bug fixes MUST include tests in the same change. Do not defer tests to a later issue or PR.
- Coverage must exercise behavior, error paths, validation, authorization, persistence, and lifecycle transitions. Tests that only execute lines without asserting meaningful behavior do not satisfy this rule.
- Do not lower the threshold, exclude packages/files, add coverage ignore directives, or move logic into unmeasured code to make the gate pass.
- Generated code and genuinely non-testable glue may only be excluded when the exclusion is explicit, narrowly scoped, documented in this file, and approved by the user.
- Backend Go coverage is measured with `go test ./... -covermode=atomic -coverprofile=coverage.out` and MUST report total statement coverage of at least 90.0%.
- Frontend coverage is measured with `npm run test:coverage` from `frontend/`. Vitest/V8 MUST report at least 90.0% for **statements, branches, functions, and lines** across `app/**/*.{ts,vue}`.
- Any new frontend logic or view behavior MUST add or extend frontend tests in the same change.
- CI MUST fail when either backend or frontend coverage is below the enforced threshold.
- Before considering implementation complete, run the relevant test suite, coverage gate, formatter/linter/type checks, and build checks.

## Repository layout

- `frontend/` is the Nuxt application root.
- `backend/` is the Go application root.
- `specs/` contains product and architecture specifications.
- Keep frontend and backend dependencies, tests, and build tooling inside their respective application roots.

## Testing conventions

- Prefer deterministic unit tests for pure logic and validation.
- Use temporary directories and temporary SQLite databases for persistence tests.
- Use local fake HTTP/process fixtures for worker/gateway tests; tests must not require a real model, GPU, external network access, or Hugging Face access.
- Test public behavior and important internal invariants, including negative/error cases.
- A regression fix must include a test that fails without the fix.
