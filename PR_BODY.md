## Description

Add provider-neutral, browser-safe live file management for the sandbox already bound to a chat session.

- expose list, download, upload, rename, and delete under the fixed `/workspace/output` root;
- keep browser paths relative and keep the session binding authoritative (the browser never supplies a provider sandbox ID);
- use one `SessionBoundManager` implementation for Docker, E2B, and Cube rather than provider-specific file managers;
- enforce component-by-component, dirfd-relative traversal with no symlink following, no special nodes, no hard-linked downloads, no overwrite, and a 16 MiB transfer limit;
- replace the unused Desktop placeholder with a Files panel using the existing authenticated session routes and sandbox side panel;
- preserve the existing artifact preview, terminal WebSocket/ticket, provider adapters, and session lifecycle.

The security/design rationale and rejected alternatives are recorded in `FILES_MINIMAL_DESIGN.md`.

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

- PASS — focused Go tests:
  - `go test ./internal/sandbox -run '^(TestCleanSessionLivePath|TestDecodeLiveFileResponseUsesFinalJSONLine|TestLiveFileResponseErrorPreservesSentinel)$' -count=1`
  - `go test ./internal/application/service -run '^TestSandboxLiveFilesService' -count=1`
  - `go test ./internal/handler/session -run '^(TestListSandboxLiveFiles|TestSandboxLiveFile|TestDownloadSandboxLiveFile|TestUploadSandboxLiveFile)' -count=1`
  - `go test ./internal/router -run '^TestConversationRoutesDeclareChatCapability$' -count=1`
- PASS — real Docker Engine 29.7.2 integration: `DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox@sha256:a28200c75e4229f1c397155fbb0aaf5540051258761408d4998d505b3b462de8 go test -tags=docker_integration ./internal/sandbox -run '^TestDockerSessionLiveFilesIntegration$' -count=1 -v -timeout=10m`
  - verified binary and Unicode round trips, nested list/rename/delete, no-overwrite conflicts, exact 16 MiB boundary, oversized rejection, tenant/session isolation, and rejection of root/intermediate/final symlinks, external hardlinks, FIFO, Unix socket, unsafe directory trees, and rename/upload escapes;
- PASS — `npm run type-check`
- PASS — `npm run check-i18n` (11/11)
- PASS — `node --test src/components/chat/SandboxSidePanel.test.mjs src/views/chat/components/SandboxFilesPanel.test.mjs` (4/4)
- PASS — `npm run build`
- PASS — `golangci-lint run --new-from-rev=upstream/main ./internal/sandbox/... ./internal/application/service/... ./internal/handler/session/... ./internal/router/...` (0 issues)
- PASS — `git diff --check`
- SKIPPED — real E2B integration: `go test -tags=e2b_integration ./internal/sandbox -run '^TestE2BIntegration' -count=1 -v -timeout=15m`; no E2B API key/template is available in this environment.
- NOT_RUN — real Cube integration; no Cube API/proxy deployment or credentials are available in this environment.
- PARTIAL — broad `go test ./internal/sandbox ./internal/application/service ./internal/handler/session ./internal/router`: the handler and router packages passed; sandbox and application-service packages hit tests outside this change that depend on unavailable Windows symlink privilege, Unix/npipe/DNS behavior, AES/runtime configuration, or other mainline fixtures. All focused live-file tests above passed.
- PARTIAL — full `npm test`: 801/806 passed. The five failures reproduce current-main/environment limitations outside this change: three missing sandbox config editor helpers, one WSL `/bin/bash` dependency on Windows, and one POSIX CLI shell-spawn assertion.
- BLOCKED — `go test ./internal/container -run '^$'` compiled the package sources but failed at the Windows link step on existing DuckDB static-library C++ runtime symbols (`std::basic_streambuf...seekpos`, `__stdio_common_vsnprintf_s`, and `__stdio_common_vswprintf`).

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

Files panel with output-root breadcrumbs, upload/refresh controls, file metadata, download, inline rename, and delete actions:

![Sandbox live files panel](docs/images/sandbox-live-files-panel.svg)
