# MySQL primary database deployment

This guide configures MySQL or Percona Server 8.0.16+ as WeKnora's **main
business database**. It covers the application connection, startup migrations,
and Helm/Compose certificate delivery. In the single-MySQL deployment modes
below, the MySQL retriever mirrors the main database TLS and timeout policy.
An independently configured retriever uses its own `MYSQL_*` TLS and timeout
settings and must be configured separately.

Use a managed database endpoint in production. The bundled
`docker-compose.mysql.yml` MySQL service is intentionally a local development
default and does not enable server TLS itself.

## Connection contract

Set `DB_DRIVER=mysql`, then configure the normal `DB_HOST`, `DB_PORT`,
`DB_USER`, `DB_PASSWORD`, and `DB_NAME` settings. The application validates
the server product, version, UTC session timezone, and strict SQL mode before
it applies MySQL migrations.

| Variable | Default | Purpose |
| --- | --- | --- |
| `DB_USE_TLS` | `false` | Enable encrypted MySQL transport. |
| `DB_TLS_SERVER_NAME` | empty | Certificate DNS name / SNI override; use when `DB_HOST` is an IP address or alias. |
| `DB_TLS_CA` | empty | PEM CA bundle path. Omit only when the issuing CA is already in the system trust store. |
| `DB_TLS_CERT`, `DB_TLS_KEY` | empty | PEM client certificate and key. They must be set together for mutual TLS. |
| `DB_TLS_INSECURE_SKIP_VERIFY` | `false` | Disables server certificate verification. Development-only; do not use in production. |
| `DB_CONNECT_TIMEOUT` | `10s` | TCP connection deadline, in Go duration syntax. |
| `DB_READ_TIMEOUT`, `DB_WRITE_TIMEOUT` | disabled (`0`) | Optional per-operation driver deadlines, in Go duration syntax. |

The equivalent retriever variables are `MYSQL_USE_TLS`,
`MYSQL_TLS_SERVER_NAME`, `MYSQL_TLS_CA`, `MYSQL_TLS_CERT`, `MYSQL_TLS_KEY`,
`MYSQL_TLS_INSECURE_SKIP_VERIFY`, `MYSQL_CONNECT_TIMEOUT`,
`MYSQL_READ_TIMEOUT`, and `MYSQL_WRITE_TIMEOUT`. The two Compose MySQL
overlays mirror unset `MYSQL_*` values from their `DB_*` counterpart. Helm
does the same when its selected retrieval driver includes `mysql`.

With `DB_USE_TLS=true`, certificate verification is enabled unless
`DB_TLS_INSECURE_SKIP_VERIFY=true` is explicitly set. A missing, unreadable,
or malformed configured TLS file is a startup error; WeKnora does not silently
fall back to plaintext.

## Docker Compose with a private CA

The external-MySQL Compose overlay mounts `DB_TLS_MOUNT_DIR` read-only at
`/run/secrets/weknora-mysql`. The repository keeps that directory empty; do
not add certificates or private keys to Git.

```dotenv
# .env
DB_DRIVER=mysql
DB_HOST=mysql-prod.example.com
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=replace-me
DB_NAME=weknora

DB_USE_TLS=true
DB_TLS_SERVER_NAME=mysql-prod.example.com
DB_TLS_MOUNT_DIR=/srv/weknora/mysql-tls
DB_TLS_CA=/run/secrets/weknora-mysql/ca.crt
# Set both only when the provider requires mutual TLS.
# DB_TLS_CERT=/run/secrets/weknora-mysql/client.crt
# DB_TLS_KEY=/run/secrets/weknora-mysql/client.key
DB_CONNECT_TIMEOUT=10s
```

The host directory contains `ca.crt` and, when required, `client.crt` and
`client.key`. Restrict it to the deployment account (`0700` directory and
`0600` private key are sensible defaults). Render before starting so an
incorrect mount or interpolated value is visible:

```bash
docker compose -f docker-compose.yml -f docker-compose.mysql.external.yml config
docker compose -f docker-compose.yml -f docker-compose.mysql.external.yml up -d
```

For a public CA, leave `DB_TLS_CA` empty; no certificate files are required,
and the empty read-only certificate mount may remain.
For local development with the overlay's built-in MySQL, use
`docker-compose.mysql.yml` and leave `DB_USE_TLS=false`; that is a different
overlay from `docker-compose.mysql.external.yml` and deliberately sets
`DB_HOST=mysql`.

## Helm with an existing TLS Secret

The chart never serializes certificate material into `values.yaml`. Create an
existing Secret in the release namespace, then configure only its name and
filenames. The app Deployment mounts it read-only only when
`database.driver=mysql` and `database.mysql.tls.enabled=true`.

```bash
kubectl -n weknora create secret generic weknora-mysql-tls \
  --from-file=ca.crt=/secure/path/ca.crt \
  --from-file=client.crt=/secure/path/client.crt \
  --from-file=client.key=/secure/path/client.key
```

For CA-only TLS, omit the last two `--from-file` flags and leave `certFile`
and `keyFile` empty:

```yaml
# values-mysql.yaml
database:
  driver: mysql
  mysql:
    host: mysql-prod.example.com
    port: "3306"
    timeouts:
      connect: "10s"
      read: ""
      write: ""
    tls:
      enabled: true
      serverName: mysql-prod.example.com
      insecureSkipVerify: false
      secretName: weknora-mysql-tls
      mountPath: /run/secrets/weknora-mysql
      caFile: ca.crt
      certFile: ""
      keyFile: ""
```

For mutual TLS, add `client.crt` and `client.key` to the Secret and set
`certFile: client.crt` plus `keyFile: client.key`.

Then render before applying:

```bash
helm template weknora ./helm -f values-mysql.yaml > rendered.yaml
helm upgrade --install weknora ./helm -n weknora --create-namespace \
  -f values-mysql.yaml
```

The chart rejects a certificate without its matching key, and rejects TLS file
names without `secretName`. Public-CA TLS can set `enabled: true` with an empty
`secretName` and all file names empty.

## Migrations and verification

Startup migration is the preferred production path because it uses the same
connection configuration as the running application, including a custom SNI
name. MySQL migration failures are fail-closed; see
[migration troubleshooting](./migration-troubleshooting.md) before forcing a
recorded version.

`make migrate-up` uses `scripts/migrate.sh`. Its golang-migrate MySQL driver
uses `tls=true` for system roots, `tls=skip-verify` for the explicit insecure
mode, and its documented `x-tls-ca`, `x-tls-cert`, and `x-tls-key` parameters
for a private CA or mutual TLS. The script rejects `DB_TLS_SERVER_NAME`: that
CLI driver has no safe custom-SNI option. Use startup migrations, or make
`DB_HOST` match the certificate DNS name. The script also rejects client TLS
credentials without `DB_TLS_CA`, because that driver cannot combine them with
system roots.

After deployment, check the application logs for the MySQL session validation
and migration result. A database operator can independently confirm the
session contract:

```sql
SELECT VERSION(), @@version_comment, @@SESSION.time_zone, @@SESSION.sql_mode;
SHOW STATUS LIKE 'Ssl_cipher';
```

`Ssl_cipher` must be non-empty for a TLS session. Do not paste `DB_PASSWORD`,
private-key contents, or a full DSN into tickets or CI logs.
