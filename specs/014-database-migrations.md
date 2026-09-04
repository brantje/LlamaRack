# 014 — Database Migrations

Status: Draft

Related issue: #118

## 1. Purpose

LlamaRack uses **`github.com/pressly/goose/v3`** as the sole database migration framework for SQLite.

Goose owns:

- migration version tracking (`goose_db_version`);
- migration ordering;
- applying migrations exactly once;
- transactional execution where supported;
- startup upgrade execution.

LlamaRack owns only the migration contents and the application-specific startup/error policy.

## 2. Layout

```text
backend/internal/database/
  database.go
  migrations.go
  migrations/
    00001_baseline.sql
  testdata/
    pre10_current.sql
```

Migrations are embedded into the manager binary with `//go:embed` so production containers do not require an external migrations directory.

`database.Open` is responsible for opening/configuring SQLite and invoking the Goose runner. It must not accumulate per-column/per-table historical compatibility code.

## 3. Schema source of truth

- `backend/internal/database/migrations/*.sql` is the schema history source of truth.
- Fresh databases and upgrades use the same embedded Goose history.
- Do not maintain a second giant `CREATE TABLE IF NOT EXISTS` initializer that can drift from migrations.

## 4. Baseline (1.0)

`00001_baseline.sql` is the immutable 1.0 baseline. It includes all durable tables previously spread across `database.go`, auth OIDC tables, and observability playground tables.

After the baseline lands:

- every schema change is a new immutable Goose migration;
- never edit an already-released migration;
- prefer SQL migrations; use a Goose Go migration only when SQL cannot express the change safely.

## 5. Supported pre-1.0 upgrade input

Exactly **one** supported pre-goose snapshot is accepted:

- all 21 core tables from current pre-1.0 `database.Open` schema, including typed `api_keys`, `instances.max_pending_requests`, and `service_accounts.hidden`;
- optional absence of `oidc_providers`, `external_identities`, and `playground_lifecycle_events`.

On first startup after adoption:

1. classify the database as the supported legacy snapshot;
2. create any missing optional tables with the exact baseline DDL;
3. stamp Goose version `1` without re-running the baseline SQL;
4. run Goose `Up` for any newer embedded migrations.

Unsupported inputs fail clearly instead of guessing:

- untyped/legacy `api_keys` without `key_type`;
- missing required core tables or required columns;
- any other historical development variant.

## 6. Startup policy

`database.Open`:

1. creates the parent directory;
2. opens SQLite with WAL, foreign keys, and busy timeout;
3. classifies the database (`empty`, `goose-managed`, `supported legacy`, `unsupported`);
4. refuses databases whose Goose version is newer than the embedded migration set;
5. runs Goose `Up` before returning the connection;
6. logs the resulting schema version.

Startup fails visibly when migration fails.

## 7. Backup and restore

Before upgrading across releases:

1. stop the manager;
2. copy the database directory, including `manager.db` and any `-wal`/`-shm` sidecars;
3. restore the copied files before starting the target release.

Automatic migrations preserve durable configuration unless a release note documents an intentional removal. Destructive resets require explicit operator action.

## 8. Unsupported / newer databases

- no downgrade migrations in 1.x;
- a database newer than the running binary is refused safely;
- unsupported legacy schemas are rejected with `ErrUnsupportedLegacySchema`.

## 9. Tests

Automated migration tests live in `backend/internal/database/migrations_test.go` and run in normal backend CI. They cover:

- fresh empty database migration to latest;
- idempotent reopen;
- supported pre-1.0 fixture upgrade with durable state preserved;
- unsupported legacy rejection;
- newer-version refusal;
- embedded migration upgrades (`1 -> 2` via test-only migration);
- failed migration rollback and retry.

## 10. 1.0 release verification checklist

Before tagging `1.0.0`:

1. start from the supported pre-1.0 fixture;
2. boot the release candidate;
3. verify durable users/auth/API keys/models/instances/settings/observability state;
4. exercise login, model/instance management, and inference;
5. restart the manager and confirm no migration reruns;
6. upgrade the RC database to a build containing an additional Goose migration and verify the `1.x` upgrade path.
