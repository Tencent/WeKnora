# BrowserSkill downstream patches

The extension and daemon are based on official `main` commit
`fa953dc6fcd868827b93164e3bea26198e691224` (after `ext-v0.3.0`). Both still
report version `0.3.0`; the exact source baseline is recorded in
`scripts/browserskill-release.json`. The daemon is built with
`cargo build --locked --release -p bsk` and includes our navigation-response
deadline patch, so the official CLI 0.3.0 binary is not an equivalent replacement.
Native builds require Rust/Cargo and a C compiler (plus CMake on Linux).
Docker builds on the target architecture.

The patches include extension fixes and a daemon navigation-response deadline fix.
`scripts/build_browserskill.sh` applies the following patches in lexical order:

| Patch | Purpose | Removal condition |
| --- | --- | --- |
| `01-browser-read-reliability.patch` | Bound CDP reads, prevent overlapping timed-out screenshots, reuse frame discovery configuration | Equivalent upstream behavior passes the background rendering and timeout regressions |
| `02-gateway-task-controls.patch` | WeKnora preview and focus side channel; drain previews before native session stop | Upstream provides equivalent UI APIs and WeKnora migrates to them |
| `04-task-popup-ownership.patch` | Attribute navigation targets to the active agent action; allow consent-based in-place borrowing inside the task window | Upstream supports this popup lifecycle without weakening per-tab authorization |
| `05-last-tab-lifecycle.patch` | Keep a temporary owned blank tab when an agent closes the last remote task tab; normal session stop removes it | Upstream distinguishes agent last-tab cleanup from user window closure |
| `06-navigation-response-deadline.patch` | Allow navigation lifecycle timeout results to arrive before the daemon transport deadline | Upstream adds equivalent navigation response grace |

Background execution and viewport/full-page screenshot support use upstream
PRs #249, #250 and #253. VOM sibling-context caching is upstream PR #271, so
patch 03 was removed. Redirect document tracking and cancelled-navigation
reconciliation also use upstream code (including PR #280); patch 06 now changes
only the daemon deadline. The numbering gaps preserve existing patch identities.

The preview side channel explicitly acquires upstream background execution
because it bypasses the automation dispatcher. Native session stop drains
in-flight previews before the official handler releases control and closes tabs.
Retained tasks keep the official session and debugger; turn completion only
stops preview polling in WeKnora. Do not restore the old
`shouldKeepTaskActive` callback or a second focus-emulation cache.

Remote authentication, credential storage/migration, renewal, connection settings,
and dedicated Agent Windows use upstream code. Patch 04 extends the
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

Errors use the native-shaped `{ "id": "…", "error": { "code": "…", "message": "…" } }`
envelope. These two methods are downstream APIs, not official BrowserSkill RPCs.
The official unmodified extension returns `unknown_method` for these UI requests.
Turn cleanup needs no custom RPC: retained tasks keep their session, and completed
tasks use native session stop. Keep serving the companion ZIP for preview/focus.

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

Even though the version remains 0.3.0, users must install the rebuilt ZIP.
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
BSK_GEOMETRY_CHROME=/path/to/test-chrome BSK_BACKGROUND_CHROME=/path/to/test-chrome \
  pnpm --filter @browser-skill/extension exec vitest run --maxWorkers=1 \
  src/browser-driver/__tests__/task-focus.browser.test.ts \
  src/tools/__tests__/background-execution.browser.test.ts \
  src/tools/__tests__/background-screenshot.browser.test.ts \
  src/tools/__tests__/background-full-page.browser.test.ts
cargo test --locked -p bsk daemon::ipc::tests
```

Run WeKnora's `TestRealExtension` with the built extension, pinned daemon and an
isolated Chromium profile; environment variables are documented in
`docs/browser-skill-integration.md`. It exercises actual pairing, screenshot and
input RPC, agent popup selection/read/close and in-place borrow approval/revocation, independent preview during help waits,
focus, pause/resume, retained-session continuity and completed-task cleanup.
It separately verifies that agent last-tab closure permits the next turn and
manual window closure blocks automation until explicit resume.

Upstream references: [PR #227](https://github.com/Tencent/BrowserSkill/pull/227),
[remote connection contract](https://github.com/Tencent/BrowserSkill/blob/fa953dc6fcd868827b93164e3bea26198e691224/docs/remote-extension-connection.md).
