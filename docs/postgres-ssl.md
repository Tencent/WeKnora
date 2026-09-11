# PostgreSQL SSL configuration

Set `DB_SSLMODE` in the application environment (or `.env` with Docker Compose):

```dotenv
DB_SSLMODE=require
```

Unset or empty values use `disable`, preserving existing deployments. Restart the
application after changing the setting; with Compose, recreate the app container
to apply environment changes. Helm exposes `app.env.DB_SSLMODE`.

The application connection, startup migrations and migration version query use
the same setting. `scripts/migrate.sh` preserves an explicit `sslmode` in `DB_URL`;
otherwise it adds `DB_SSLMODE` (default `disable`). `DB_URL` is a migration-script
override, not an application connection override. Keep it consistent with the app.

Supported values are `disable`, `require`, `verify-ca` and `verify-full`. Other
values fail validation. Although pgx supports `allow` and `prefer`, the pinned
lib/pq v1.10.9 migration driver does not, so these modes are rejected consistently.

## Deployment implications

- This configures the client only. The PostgreSQL server must separately enable
  TLS and have a usable certificate. The bundled local database is not automatically
  configured for TLS; setting `require` against a server without TLS fails.
- `require` requires encryption; do not use it as a guarantee of server identity.
  Use `verify-full` for certificate-chain and hostname verification. `DB_HOST`
  must match the server certificate, including when connecting through a proxy.
- For a private CA, set `PGSSLROOTCERT` to a PEM CA file accessible to the app and
  the migration process. If the server requires client certificates, also set
  `PGSSLCERT` and `PGSSLKEY`, with appropriate private-key permissions. These
  standard driver environment variables must be explicitly passed into containers
  and their files mounted; adding them to the Compose `.env` alone does not pass
  them through. Paths on the host and inside containers may differ.
- pgx and lib/pq have different certificate defaults. Supply an explicit CA path
  for verification and check both application startup and manual migrations.
  Certificate expiry, trust-chain problems or hostname mismatches can prevent
  connections and migrations. Do not depend on implicit certificate behavior in
  `require` mode.
- SQLite and separate services such as Langfuse are unaffected. No schema or data
  format changes are introduced. TLS adds connection-handshake and encryption
  overhead; its actual impact depends on connection reuse and workload.

References: [lib/pq v1.10.9 TLS implementation](https://github.com/lib/pq/blob/v1.10.9/ssl.go),
[PostgreSQL SSL documentation](https://www.postgresql.org/docs/current/libpq-ssl.html).
