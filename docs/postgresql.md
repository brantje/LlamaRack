# PostgreSQL storage

SQLite remains LlamaRack's zero-configuration default and the recommended database for the simplest single-node installation. PostgreSQL is optional and is selected only when a PostgreSQL URL is configured.

## Supported configuration

LlamaRack 1.1 qualifies PostgreSQL 17 in CI. Configure PostgreSQL with either:

```text
LLAMARACK_DATABASE_URL=postgres://user:password@host:5432/llamarack
```

or the conventional fallback:

```text
DATABASE_URL=postgres://user:password@host:5432/llamarack
```

`LLAMARACK_DATABASE_URL` takes precedence when both are set. Normal PostgreSQL URL options supported by pgx can be supplied, including TLS parameters such as `sslmode`. Database URLs are deployment secrets and should not be placed in source control or logs.

If a PostgreSQL URL is configured and the database is invalid, unreachable, or incompatible, LlamaRack fails startup. It does not silently fall back to SQLite.

## Docker Compose example

The repository includes `docker-compose.postgres.yml` as an optional override. It is not part of the default stack.

Set a password and start LlamaRack with the override:

```bash
export LLAMARACK_POSTGRES_PASSWORD='choose-a-strong-password'
docker compose -f docker-compose.yml -f docker-compose.postgres.yml up -d
```

If the password contains URL-reserved characters, provide an explicitly URL-encoded `LLAMARACK_DATABASE_URL` instead of relying on the example URL.

The PostgreSQL service uses a named volume, `llamarack-postgres`, for database persistence. The normal `/config` volume remains in use for non-database manager state such as locally stored encryption/signing material.

## Database permissions

The configured PostgreSQL role must be able to connect to the target database and own, or otherwise have sufficient privileges on, the active schema to let Goose create and alter LlamaRack tables, indexes, constraints, functions, and triggers. A dedicated database and role owned by LlamaRack is the recommended setup.

Do not expose PostgreSQL directly to untrusted networks. Network ACLs, server certificates, client authentication, and TLS policy are deployment responsibilities.

## Schema migrations

Goose remains the only production schema migration authority for both SQLite and PostgreSQL. LlamaRack applies the PostgreSQL migration set automatically during startup and keeps its logical migration versions aligned with SQLite.

LlamaRack refuses a non-empty schema that it does not recognize as LlamaRack-managed, and refuses a schema whose recorded migration version is newer than the running binary supports.

GORM `AutoMigrate` is not used.

## Backup and restore

Use normal PostgreSQL tooling and your provider's snapshot facilities. For a self-managed database, a basic logical backup can be made with:

```bash
pg_dump --format=custom --file=llamarack.dump "$LLAMARACK_DATABASE_URL"
```

Restore into an empty target database with `pg_restore` according to your PostgreSQL operational policy. Back up LlamaRack's `/config` data alongside the database because encryption/signing material stored there is not moved into PostgreSQL.

Test restores before relying on them for upgrades or disaster recovery.

## Switching database engines

LlamaRack 1.1 does **not** provide an automatic SQLite-to-PostgreSQL or PostgreSQL-to-SQLite data-transfer tool. Configuring PostgreSQL for an existing SQLite installation therefore starts against the PostgreSQL database's own state; it does not import the SQLite database.

Use SQLite for existing installations unless you have an explicit external migration/restore procedure. A supported cross-engine migration tool can be added separately.

## Redis independence

Redis, when configured, is only a non-authoritative cache. PostgreSQL does not require Redis, and Redis must not hold durable manager, authentication, scheduler, lifecycle, download, or inference state.
