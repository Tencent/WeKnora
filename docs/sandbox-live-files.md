# Safe Live Files minimal design

## Baseline and scope

- Source of truth: `upstream/main` at `5db13a131e10e8ee2105211f665412ebc13bd98e` (2026-09-10).
- Main already has provider-neutral `SessionFileStore`, `SessionBoundManager`, and Docker/E2B/Cube filesystem primitives. It also has persisted message artifacts and their preview/download UI.
- Main does not expose the live session filesystem to the browser. The sandbox panel still contains a Desktop placeholder.
- PR-02 adds only browse, upload, download, rename, delete, refresh, and breadcrumbs under `/workspace/output`.
- Out of scope: editor, VNC/Desktop, batch operations, archive/zip, audit, terminal changes, resource limits, ArtifactKind, and new provider-specific file managers.

Open PR #3146 remains merge-dirty and implements a separate Workbench service/ticket/socket/UI stack. It is competition analysis only. PR-02 will reuse current session routes, bearer/API-key authentication, ownership checks, pinned sandbox binding, side panel, and document preview instead of copying that architecture.

## A. Existing remote FS plus server-side path checks

Shape:

```text
relative browser path
  -> lexical join with /workspace/output
  -> SessionFileStore List/Stat/Read/Write/Remove
```

Advantages:

- smallest code change;
- reuses all current provider data-plane file transfers.

Rejected as the security boundary because lexical checks cannot stop intermediate symlink traversal. `path.Clean` plus prefix matching proves only the spelling of the path. The current Docker `Stat` documentation explicitly records that `/workspace/output/link-to-etc/passwd` follows the intermediate link. A preflight `Stat` followed by `ReadFile` also has a time-of-check/time-of-use race.

## B. Fixed in-sandbox safe helper

Shape:

```text
authenticated session route
  -> pinned SessionBoundManager
  -> fixed Python helper through RemoteSandboxClient.Exec
  -> open /workspace/output with O_DIRECTORY | O_NOFOLLOW
  -> component-by-component dirfd traversal with O_NOFOLLOW
  -> operation by *at syscall / dir_fd
```

The helper is a server constant, not user-authored shell. A JSON request travels on stdin; path components never enter a shell command. It uses Linux dirfds as the capability boundary:

- open the root itself with `O_NOFOLLOW`, rejecting an output-root symlink;
- reject absolute, empty component, `.`, `..`, NUL, backslash, and invalid UTF-8 at the Go boundary;
- open every intermediate directory relative to its parent fd with `O_DIRECTORY|O_NOFOLLOW`;
- inspect the final entry with `follow_symlinks=false`;
- accept only regular files and directories appropriate to the operation;
- reject symlinks, FIFO, socket, block/character device, and unknown node types;
- upload through an exclusive temporary regular file in the already-open parent, then atomically publish it without overwriting an existing name;
- rename with source and destination parent dirfds; destination collision is an error;
- recursively delete only after fd-relative traversal confirms the tree contains no special node or symlink.

Advantages:

- one implementation for Docker, E2B, and Cube;
- no provider-specific `*FileManager` family;
- the check and mutation use the same directory handles, removing the prefix and intermediate-symlink TOCTOU problem;
- adding a backend only requires the already-existing Exec capability.

Costs:

- transfers are JSON/base64 rather than the provider file API, so v1 imposes a strict per-file size cap;
- depends on Python 3 in the standard sandbox image/template, already required by the current agent sandbox runtime.

Selected for PR-02.

## C. Optional capability/interface extension

Adding provider `Rename`/safe-open methods to `RemoteSandboxClient` would force every adapter and fake to implement a new contract, while still not giving E2B/Cube a documented no-follow guarantee. Three adapter implementations would duplicate the same security policy and produce the forbidden Docker/E2B/Cube file-manager split.

PR-02 instead adds one narrow session-facing `SessionLiveFileManager` capability implemented by `SessionBoundManager`. It does not replace `SessionFileStore`; internal agent/attachment/artifact code continues using the existing interface. The application service narrows to the live-files capability only for browser operations.

## Security and identity invariants

```text
browser session id
  -> current authenticated tenant/user
  -> SessionService.GetSession ownership check
  -> SessionSandboxPinner config id
  -> tenant-scoped SessionBoundManager
  -> binding lookup by tenant + session
  -> opaque provider handle
```

- The browser never supplies sandbox/container/PTY IDs.
- API paths are relative POSIX paths. The browser-visible root is always `/workspace/output` and is never accepted as input.
- Empty path denotes the root only for listing.
- The service is lookup-only: no live binding returns a not-found/conflict response and never provisions a sandbox.
- Tenant A + Session B and Tenant B + Session A both fail before any provider operation. Non-owned and nonexistent sessions share the same not-found response.
- Download responses use `private, no-store`, attachment disposition, an extension-derived safe content type, and `X-Content-Type-Options: nosniff`.
- Upload/read maximum is 16 MiB of file bytes. Request-body overhead is bounded separately.
- Names returned by the helper are relative to the fixed root; absolute provider paths never reach the browser.
- Browser JSON uses `type=directory` (not the internal remote listing alias `dir`).
- GET list/download and other live-file operations peek the bound sandbox state first. A paused, transitioning, or list-miss binding returns a conflict and does **not** Connect, so opening the Files tab cannot resume (and re-bill) an E2B/Cube/Docker instance.
- Listings cap at 1024 entries; delete trees cap at 64 depth and 8192 entries.

## API

All routes stay under the existing authenticated session group:

| Method | Route | Input | Result |
|---|---|---|---|
| GET | `/sessions/:id/sandbox/files` | query `path` (empty=root) | one directory level |
| POST | `/sessions/:id/sandbox/files` | multipart `path`, `file` | upload, no overwrite |
| GET | `/sessions/:id/sandbox/files/content` | query `path` | attachment download |
| PATCH | `/sessions/:id/sandbox/files` | JSON `source`, `target` | atomic rename, no overwrite |
| DELETE | `/sessions/:id/sandbox/files` | query `path` | delete file or safe directory tree |

Application errors distinguish invalid path (400), missing file (404), collision or no live sandbox (409), size limit (413), and provider/internal failure (500) without exposing provider paths.

## UI

Replace the Desktop placeholder with Files, preserving the existing panel hierarchy:

```text
Artifacts | Terminal | Files
```

`SandboxFilesPanel.vue` owns only live-file state:

- lazy load when Files becomes active;
- breadcrumb navigation rooted at “Output”;
- manual refresh;
- folder open;
- regular-file download;
- upload into current directory;
- inline rename;
- delete confirmation;
- empty/loading/error states;
- special entries, if ever returned, are non-actionable.

It does not duplicate `document-preview.vue`; v1 downloads rather than adding a second preview pipeline.

## Expected production files

- `internal/sandbox/capabilities.go`: narrow session live-file capability.
- `internal/sandbox/session_live_files.go`: relative path validation, fixed helper protocol, lookup-only operations, size limits, error mapping.
- `internal/application/service/sandbox_live_files_service.go`: pinned manager resolution and browser-safe DTOs.
- `internal/handler/session/sandbox_live_files.go`: authenticated HTTP handlers and bounded multipart/download transport.
- `internal/handler/session/handler.go`, `internal/container/container.go`, `internal/router/routes_chat.go`: normal DI and existing session-route registration.
- `frontend/src/api/chat/sandbox-files.ts`: typed API calls.
- `frontend/src/views/chat/components/SandboxFilesPanel.vue`: focused browser UI.
- `frontend/src/components/chat/SandboxSidePanel.vue`, `frontend/src/composables/useChatSandboxPanel.ts`: replace Desktop tab/placeholder.
- locale files: Files labels only.
- `docs/api/session.md`, `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml`: HTTP surface.
- `client/session_sandbox_files.go`: official Go SDK helpers.

No Docker/E2B/Cube adapter file should need production changes.

## Test plan

Unit/service/handler:

- path parser: root, nested path, absolute, traversal, nested traversal, separators, NUL/control, overlong;
- list and nested list;
- binary upload/download round trip;
- rename and collision;
- delete file/tree, missing entry;
- 16 MiB boundary and oversized request;
- root/intermediate/final symlink;
- upload-parent and rename source/target escape;
- FIFO/socket/special node;
- tenant A/session B and tenant B/session A negative cases;
- no binding does not provision;
- content-disposition/no-store/nosniff headers.

Real Docker integration:

- create one actual session sandbox from the standard image;
- list/upload/download/rename/delete nested files through `SessionBoundManager`;
- create symlink/FIFO attack fixtures through a trusted test exec and prove every browser operation refuses them;
- prove another tenant context cannot access the first tenant/session binding.

Frontend:

- API relative-path encoding and multipart behavior;
- tab order and Desktop removal;
- breadcrumb, refresh, upload, rename, delete, and error-state source/behavior tests;
- `npm run type-check`.

Before PR: gofmt, focused tests, real Docker integration, `git diff --check`, diff-scoped lint, UI screenshot/recording, self-review, fetch/rebase latest `upstream/main`, and rerun affected tests.
