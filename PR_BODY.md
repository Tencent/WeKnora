## Description

Adds provider-neutral command audit to the existing interactive session terminal. Bash prompt integration emits authenticated private markers containing the committed history entry and real exit status; the current WebSocket bridge incrementally removes those markers before xterm output and writes a sanitized `sandbox.terminal_command` row through the native `AuditLogService`.

The implementation keeps the existing terminal ticket, session binding, WebSocket, xterm UI, provider sessions, auth recheck, and lifecycle. It also adds bounded case-insensitive search over structured audit details and exposes sanitized commands in the existing tenant audit drawer. No `CommandTerminal`, second audit store, or raw-keystroke command reconstruction is introduced.

Secret-bearing assignments, CLI flags, authorization headers, bearer values, and URL user-info passwords are redacted before persistence. Audit writes use a bounded asynchronous queue so storage latency cannot block PTY output. `TERMINAL_AUDIT_DESIGN.md` documents the protocol, provider/reconnect behavior, test plan, and the honest limitations of shell-level audit.

## Type of Change

- [x] ✨ New feature
- [ ] 🐛 Bug fix
- [ ] 💥 Breaking change
- [x] 📚 Documentation update
- [ ] 🎨 Refactor
- [ ] ⚡ Performance improvement
- [x] 🧪 Test
- [ ] 🔧 Configuration / Build / CI

## Related Issue

N/A — Rhino Bird Topic 2 sandbox workbench.

## Testing

- PASS — focused Go tests after rebasing onto `upstream/main@8c2c97612784b7ef4fde5a23e31884b60b6b1116`:
  - `go test ./internal/application/service -run TestSandboxTerminalAuditToken -count=1`
  - `go test ./internal/application/repository -run TestAuditLogRepositoryListFiltersKnowledgeBaseScope -count=1`
  - `go test ./internal/handler -run 'Test(AuditLogHandler|BoundedAuditSearch|KnowledgeBaseActivityHandler|SystemAuditLogHandler)' -count=1`
  - `go test ./internal/handler/session -run 'Test(TerminalAudit|SanitizeTerminalAudit|TerminalBridge)' -count=1`
  - `go test ./internal/types -run TestAuditAction -count=1`
- PASS — the same five focused Go test groups with `go test -race`.
- PASS — real Docker Engine 29.7.2 TTY integration using image `wechatopenai/weknora-sandbox:dev` (`sha256:a28200c75e4229f1c397155fbb0aaf5540051258761408d4998d505b3b462de8`): `DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox:dev go test -tags=docker_integration ./internal/handler/session -run TestTerminalAuditDockerPTYIntegration -count=1 -v`. Observed login-shell markers for `true` exit 0 and `false` exit 1, Unicode/ANSI preservation, secret redaction, marker stripping, and clean teardown.
- PASS — `npm test -- src/api/tenant/audit-log.test.mjs src/views/chat/components/SandboxTerminal.test.mjs` with Git for Windows Bash: 9/9 tests.
- PASS — `npm run type-check`.
- PASS — `npm run check-i18n`: 11/11 tests.
- PASS — `npm run build`: 6,503 modules transformed; completed in 2m31s. Vite warned that local Node 20.18.0 is below its recommended 20.19+/22.12+ version, but the build completed successfully.
- PASS — `golangci-lint run --new-from-rev upstream/main ./internal/application/repository ./internal/application/service ./internal/handler ./internal/handler/session ./internal/types`: 0 issues.
- PASS — `git diff --check`.
- FAIL (unrelated baseline) — `go test ./internal/application/service ./internal/application/repository ./internal/handler ./internal/handler/session ./internal/types`: existing `TestPutTenantParserConfigAdminPreservesRedactedSecrets` expected HTTP 200 and received 400; all PR-focused tests above pass.
- FAIL (local environment) — `go test ./internal/container ./internal/router -run '^$'`: `internal/router` compiled, while `internal/container` could not compile the `sqlite-vec` CGO dependency because local `sqlite3.h` is unavailable.
- NOT_RUN — full `go test ./...` and full frontend test suite; focused changed-package/component coverage, race tests, production frontend build, and real Docker integration were run instead.

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

NOT_RUN — no screenshot or recording was captured. The only UI change is a search field and sanitized terminal-command summary in the existing tenant audit drawer; behavior is covered by frontend static tests, type-check, i18n audit, and production build.
