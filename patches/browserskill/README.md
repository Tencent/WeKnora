# BrowserSkill downstream patches

The extension is based on the official `ext-v0.3.0` release at
`75e2c64abaf4b7cc75682d94b0c1fd5db0cbd5e5`. The daemon is built with
`cargo build --locked --release -p bsk` from the same commit, recorded in `scripts/browserskill-release.json`. The published CLI 0.2.1
binary lacks `scroll_to`, `wheel`, `focus`, and `blur`; protocol handshake success
does not establish command compatibility. Native builds require Rust/Cargo and a C
compiler (plus CMake on Linux). Docker builds on the target architecture.

The patches include extension fixes and a daemon navigation-response deadline fix.
`scripts/build_browserskill.sh` applies the following patches in lexical order:

| Patch | Purpose | Removal condition |
| --- | --- | --- |
| `01-browser-read-reliability.patch` | Bound CDP reads, prevent overlapping timed-out screenshots, keep explicitly controlled remote pages rendering, reuse frame discovery configuration | Equivalent upstream behavior passes the background rendering and timeout regressions |
| `02-gateway-task-controls.patch` | WeKnora preview, focus, and idle side channel; coordinate screenshots and automation before releasing debugger attachments | Upstream provides equivalent UI and lifecycle APIs and WeKnora migrates to them |
| `03-vom-render-performance.patch` | Bound repeated sibling-context scans on pages with many repeated action buttons | Equivalent upstream rendering passes the semantic and work-count regression |
| `04-task-popup-ownership.patch` | Attribute navigation targets to the active agent action; allow consent-based in-place borrowing inside the task window | Upstream supports this popup lifecycle without weakening per-tab authorization |
| `05-last-tab-lifecycle.patch` | Keep a temporary owned blank tab when an agent closes the last remote task tab; normal session stop removes it | Upstream distinguishes agent last-tab cleanup from user window closure |
| `06-navigation-lifecycle.patch` | Follow committed main-frame redirect loaders; allow navigation lifecycle timeout results to arrive before the daemon transport deadline | Upstream passes redirect, stale-loader and visible-error timeout regressions |

Remote authentication, credential storage/migration, renewal, connection settings,
and dedicated Agent Windows use upstream code. The fourth patch extends the
upstream borrow/return flow while preserving explicit consent and per-tab isolation.
Do not restore the old `remote-extension-connection.patch`: upstream PR #227
already incorporates its remote connection support with different ownership and
window semantics.

## Companion UI contract

Only authenticated remote sockets handle these extra request frames. They do not
enter the native daemon's automation queue:

```json
{"id":"wk-ui-unique","method":"gateway.task_preview","params":{"session_id":"server-owned-session"}}
```

- `gateway.task_preview`: returns `image_base64`, `format: "jpeg"`, `tab_id`,
  `title`, and `captured_at`. The encoded image is at most 640 pixels wide.
  Captures are coalesced and restricted to a concrete owned tab. Authorization,
  document revision and debugger identity are checked before returning a frame.
  UI previews include the extension's control and help overlays so periodic
  captures do not hide and restore them in the user's browser. Agent screenshots
  still suppress overlays to preserve unobstructed page content.
- `gateway.task_focus`: explicitly activates an existing owned tab and its window;
  returns `{ "focused": true }`. It never creates a session.
- `gateway.task_idle`: prevents further UI captures, waits for in-flight work,
  clears references and detaches the session debugger without closing tabs;
  returns `{ "released": true }`. A subsequent native operation resumes capture.
  Native session stop uses the same barrier.

Errors use the native-shaped `{ "id": "…", "error": { "code": "…", "message": "…" } }`
envelope. These three methods are downstream APIs, not official BrowserSkill RPCs.
The official unmodified extension returns `unknown_method`: WeKnora can still
stop completed tasks with native RPC, but retaining a task while releasing control
requires this companion build. Keep serving the companion ZIP in WeKnora settings.

## Intentional behavior changes

- Remote tasks use official dedicated Agent Windows. The old background-tab-group
  preference is no longer used; no `tabGroups` permission is added.
- Only Chrome navigation-target events from an owned source during an agent action
  grant popup ownership (including `rel=noopener`). A bounded 100 ms event tail
  follows the input acknowledgement. Window membership/opener metadata alone do
  not authorize tabs; delayed or unattributed targets use the borrow flow.
- Unowned tabs already inside the task window can be borrowed with normal browser
  approval. They remain borrowed user tabs, are returned without being closed,
  and lose access immediately on return. Other task windows remain isolated.
- Closing the last owned tab through `tab_close` keeps a temporary owned blank tab
  until session stop, preventing Chrome window removal from being misreported as
  a human interruption. Actual user window closure still pauses the task.
- Human help and borrowing follow the upstream focus/confirmation behavior.
- Remote upload/download remain unsupported, as defined upstream.

Upgrade users by replacing the existing unpacked extension directory and reloading
it after ending active tasks. Keeping the extension ID allows upstream migration
from legacy Chrome storage into extension-origin IndexedDB. Installing under a new
ID requires pairing again. Old saved tab groups are not removed automatically.

## Validation

Apply patches to a clean pinned checkout, install frozen dependencies and run:

```sh
pnpm --filter @browser-skill/extension exec wxt prepare
pnpm --filter @browser-skill/extension compile
pnpm --filter @browser-skill/extension test
pnpm --filter @browser-skill/vom test
pnpm ext:build:zip
BSK_GEOMETRY_CHROME=/path/to/test-chrome pnpm --filter @browser-skill/extension exec vitest run src/browser-driver/__tests__/task-focus.browser.test.ts
```

Run WeKnora's `TestRealExtension` with the built extension, pinned daemon and an
isolated Chromium profile; environment variables are documented in
`docs/browser-skill-integration.md`. It exercises actual pairing, screenshot and
input RPC, agent popup selection/read/close and in-place borrow approval/revocation, independent preview during help waits,
focus, pause/resume, retained-task debugger release and completed-task cleanup.
It separately verifies that agent last-tab closure permits the next turn and
manual window closure blocks automation until explicit resume.

Upstream references: [PR #227](https://github.com/Tencent/BrowserSkill/pull/227),
[remote connection contract](https://github.com/Tencent/BrowserSkill/blob/75e2c64abaf4b7cc75682d94b0c1fd5db0cbd5e5/docs/remote-extension-connection.md).
