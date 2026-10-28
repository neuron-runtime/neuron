# Storage Migrations, Backup, and Restore

## Migrations
- Storage backends use a versioned migration framework in `nore/internal/storage/migrate/`.
- The framework tracks applied versions in a `schema_migrations` table and applies pending migrations transactionally where possible.
- If the database schema version is newer than the binary supports, startup is refused with an error (do not downgrade).
- Migrations run during backend initialization before the store is returned/pinged.

## SQLite
- Default location: `~/.neuron/nore/nore.db` (or configured data dir/path).
- Backup: copy the database file while the store is closed, or use SQLite's backup mechanism. For a live database, ensure no active writes or copy the WAL-consistent snapshot.
- Restore: replace the database file with a backup copy and ensure correct permissions.

## PostgreSQL
- Configure via `NEURON_DATABASE_URL` or `--storage-dsn`. DSN must not be logged.
- Backup: use `pg_dump` (e.g. `pg_dump -Fc $NEURON_DATABASE_URL > backup.dump`).
- Restore: use `pg_restore` to a prepared database.
- The `schema_migrations` table tracks versions; migrations are applied on startup.

## Upgrade path from existing SQLite databases
Existing SQLite databases created before the versioned migration framework are compatible: migration v1 uses `CREATE TABLE IF NOT EXISTS` and the framework will record it in `schema_migrations` on first run.
