# Live host-port check evidence — 
Commit: `0209eb1a`

## A — standalone APP_PORT=8888, SEARXNG_PORT unset (must PASS)
```
OK: host-port check passed
exit:0
```

## B — local SearXNG enabled, APP_PORT=SEARXNG_PORT=8080 (must FATAL)
```
exit:2
```

## C — local SearXNG enabled, 8080 vs 8888 (must PASS)
```
OK: host-port check passed
exit:0
```

## D — APP_PORT=8888 SEARXNG_PORT=8888 (must FATAL)
```
exit:2
```

## E — independently deployed SearXNG (no SEARXNG_PORT), APP_PORT=8080 (must PASS)
```
OK: host-port check passed
exit:0
```

## Unit tests
```
ok  	github.com/Tencent/WeKnora/internal/runtime	0.643s
```

Note: Docker Engine was unavailable on this cloud box (apt 502 / no docker binary), so compose `--profile searxng` was not exercised live; searxng-init collision script remains profile-gated in docker-compose.yml.
