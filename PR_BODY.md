## Description

Add Docker as the third backend for the existing session-persistent interactive terminal stack. The adapter uses Docker Engine native TTY exec/attach/resize APIs, streams raw PTY bytes without `stdcopy`, reports real exit codes, refreshes the existing Docker activity marker at a bounded low frequency, and reuses the current terminal ticket, WebSocket bridge, session binding, auth recheck, and xterm UI.

A real-daemon spike found that Docker cannot attach a second transport to an already-running exec. This PR therefore adds a provider-neutral `SupportsTerminalReconnect` capability: E2B/Cube advertise true, Docker advertises false, and the existing ready frame prevents the browser from presenting a fresh Docker shell as a successful reattach.

The implementation is intentionally limited to Docker Interactive Terminal. It does not add Files, Audit, Limits, ArtifactKind, Presentation Skill, another terminal abstraction, or another WebSocket route.

Known provider limitation: closing or losing a Docker exec transport can leave the shell process inaccessible until the existing Docker idle lifecycle reclaims its session container. The client does not automatically create a replacement shell and does not claim reconnect support.

## Type of Change

- [ ] 🐛 Bug fix
- [x] ✨ New feature
- [ ] 💥 Breaking change
- [x] 📚 Documentation update
- [ ] 🎨 Refactor
- [ ] ⚡ Performance improvement
- [x] 🧪 Test
- [ ] 🔧 Configuration / Build / CI

## Related Issue

N/A — Rhino Bird Topic 2 sandbox workbench.

## Testing

- PASS — `go test ./internal/sandbox -run "Test(Docker.*Terminal|.*Terminal.*Docker|OpenSessionTerminal|SessionTerminalManager|EmitTerminal|TerminalTTL|EffectiveTerminal|StartTerminal|Cube.*Terminal)" -count=1 -v`
- PASS — `go test ./internal/application/service -run 'Test.*Terminal' -count=1 -v`
- PASS — `go test ./internal/handler/session -run 'TestTerminal' -count=1 -v`
- PASS — `go test -race ./internal/sandbox -run '^TestDockerTerminal' -count=1 -timeout=3m`
- PASS — `DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox:dev go test -tags=docker_integration ./internal/sandbox -run '^TestDockerInteractiveTerminalIntegration$' -count=1 -v -timeout=5m`
  - Docker Desktop 4.88.1 / Engine 29.7.2 / API 1.55
  - image `wechatopenai/weknora-sandbox@sha256:a28200c75e4229f1c397155fbb0aaf5540051258761408d4998d505b3b462de8`
  - observed raw ANSI/Unicode, stdin, Ctrl-C, resize `123x41`, cwd/env/user, exit code `23`, stream lifetime beyond a 2-second short-RPC timeout, and `active-idle=false` after the 3-second idle TTL
- PASS — `npm run type-check`
- PASS — `npm test -- src/composables/useSandboxTerminal.reconnect.test.mjs`
- PASS — `golangci-lint v2.12.2 run --new-from-rev=upstream/main ./internal/sandbox/...` (`0 issues`)
- BLOCKED (environment) — full-root diff lint reached unchanged SQLite CGo dependencies and could not find the host `sqlite3.h`; a `CGO_ENABLED=0` retry reached unchanged `pg_query` CGo symbols. No new lint issue remained after the two reported PR findings were fixed.
- BLOCKED (environment/baseline) — `go test ./internal/sandbox ./internal/application/service ./internal/handler/session -count=1`: handler passed; sandbox failed unchanged Windows symlink privilege, detected `npipe` Docker-host validation, and local DNS mapping `api.e2b.dev` to `198.18.0.30`; service full-package tests include the same `npipe` baseline plus unrelated repository/service failures. The focused terminal suites above passed.
- PASS — `git diff --check`

## Checklist

- [x] `git diff --check origin/main...HEAD` passes
- [x] Changed source files are formatted
- [x] Targeted tests for the changed packages/components pass
- [x] Diff-scoped lint passes where applicable (for Go: `golangci-lint run --new-from-rev=origin/main ./...`)
- [x] Full-repository checks were run, or any unrelated/environment-dependent failures are documented above
- [x] Self-reviewed the code
- [x] Added/updated tests covering the change
- [x] Updated related documentation (README, `docs/`, Swagger annotations, etc.)
- [x] Breaking changes are clearly called out in the description above

## Screenshots / Recordings

N/A — protocol/backend change; verified through focused WebSocket/frontend tests and a real Docker Engine integration test.
