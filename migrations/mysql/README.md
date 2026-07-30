# MySQL Migrations

`000074_baseline.up.sql` is the MySQL 8 baseline for new WeKnora deployments.
It creates the complete current application schema and is the file selected by
the application when `DB_DRIVER=mysql`.

`00-init-db.sql` predates complete MySQL backend support and creates only a
small subset of tables. It is kept for historical reference only and is not a
golang-migrate migration file.

For every schema version after 74, add a matching numbered MySQL migration to
this directory. Do not replay PostgreSQL migration history against MySQL.
