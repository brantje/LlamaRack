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

## 5. Startup policy

`database.Open`:

1. creates the parent directory as `0700` and repairs an existing parent to `0700` when it is not `.` or `/`;
2. opens SQLite with WAL, foreign keys, and busy timeout;
3. classifies the database (`empty`, `goose-managed`, or `unsupported`);
4. refuses databases whose Goose version is newer than the embedded migration set;
5. runs Goose `Up` before returning the connection;
6. logs the resulting schema version;
7. restricts the database file and any `-wal`/`-shm` sidecars to `0600`, and fails startup if the filesystem cannot honor those modes.

Supported inputs:

- an empty SQLite file (fresh install);
- a database already managed by embedded Goose migrations.

Unsupported inputs fail clearly instead of guessing:

- any SQLite file that contains application tables but no `goose_db_version` history;
- a database newer than the running binary.

There is no pre-Goose upgrade path. Operators with incompatible databases must recreate the database or restore a Goose-managed backup.

Startup fails visibly when migration fails.

## 6. Backup and restore

Before upgrading across releases:

1. stop the manager;
2. copy the database directory, including `manager.db` and any `-wal`/`-shm` sidecars;
3. restore the copied files before starting the target release.

Automatic migrations preserve durable configuration unless a release note documents an intentional removal. Destructive resets require explicit operator action.

## 7. Unsupported / newer databases

- no downgrade migrations in 1.x;
- a database newer than the running binary is refused safely;
- unmanaged schemas are rejected with `ErrUnsupportedDatabaseSchema`.

## 8. Tests

Automated migration tests live in `backend/internal/database/migrations_test.go` and run in normal backend CI. They cover:

- fresh empty database migration to latest;
- idempotent reopen;
- unmanaged schema rejection;
- newer-version refusal;
- embedded migration upgrades (`1 -> 2` via test-only migration);
- failed migration rollback and retry.

## 9. 1.0 release verification checklist

Before tagging `1.0.0`:

1. boot the release candidate against a fresh database;
2. verify schema creation through Goose migrations;
3. exercise login, model/instance management, and inference;
4. restart the manager and confirm no migration reruns;
5. upgrade the RC database to a build containing an additional Goose migration and verify the `1.x` upgrade path.
