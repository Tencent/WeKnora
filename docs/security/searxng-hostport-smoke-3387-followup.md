# Live Docker + host-port evidence — 2026-09-19 21:32 CST

Commit: `49383691`
Docker: `27.5.1` (static install, storage-driver=vfs)

## Process-level (same helper as cmd/server)
See earlier smoke: standalone APP_PORT=8888 PASS; enabled collision FATAL; distinct PASS.

## Docker Compose `--profile searxng` / `searxng-init`

### F1 — APP_PORT=8888 SEARXNG_PORT=8888 (must FAIL)
```
ERROR: SEARXNG_PORT=8888 collides with APP_PORT=8888.
On Linux this makes localhost:8888 hit SearXNG instead of WeKnora.
Keep SEARXNG_PORT at 8888 (or another free port), distinct from APP_PORT.
exit:1
```

### F2 — APP_PORT=8080 SEARXNG_PORT=8888 (must PASS)
```
exit:0 (searxng-init completed)
```

### F3 — no searxng profile
searxng / searxng-init not selected without `--profile searxng`.
