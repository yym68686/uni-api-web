# Control Database Migrations

`control_migrations.go` applies ordered, immutable migrations and records each
version, SHA-256 checksum, and application timestamp in
`console_schema_migrations`. Startup with an applied version only reads this
ledger. Do not add startup DDL or modify an applied SQL/lock inventory file.

For a schema change, add a numbered SQL file, an explicit list of every existing
relation it can lock (including foreign-key dependencies and backfill sources),
and a `controlMigrations()` entry. Keep changes compatible with the previous
application version during rolling deployment. Destructive changes require a
separate deployment after older writers have retired.

The first version adopts existing installations without deleting data. A
transaction advisory lock serializes new migrators. Before DDL, all listed
existing tables are locked using `ACCESS EXCLUSIVE NOWAIT`. If any table is
busy, the entire transaction rolls back before retrying; no partial table lock
set is held while waiting for older DML. Catalog locks also have a short timeout.
Startup's context bounds the wait. Failed migrations never record their version.
The currently serving instance can continue while a new instance waits or fails
its startup.

Database-backed tests use `TEST_CONTROL_DATABASE_URL` and create isolated test
schemas. Coverage includes concurrent adoption, lock inversion with an older
writer, cancellation, transactional rollback, checksum drift, and read-only
startup with an applied schema.
