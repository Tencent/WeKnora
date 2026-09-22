# BrowserSkill downstream patches

The extension and daemon are based on official `main` commit
`c61eb7e4b1a5785d51b1a316dbf57686fecbf33d` (after `ext-v0.3.0`, including the
merged upstream PRs #291, #296 and #297). Both still report version `0.3.0`;
the exact source baseline is recorded in `scripts/browserskill-release.json`.
The daemon is built with `cargo build --locked --release -p bsk` from the same
commit. The published CLI 0.3.0 binary and the published 0.3.0 extension ZIP
predate this baseline and are not equivalent replacements. Native builds
require Rust/Cargo and a C compiler (plus CMake on Linux). Docker builds on the
target architecture.

`scripts/build_browserskill.sh` applies the following patches in lexical order:

| Patch | Purpose | Removal condition |
| --- | --- | --- |
| `01-browser-read-reliability.patch` | Bound CDP reads, prevent overlapping timed-out screenshots, reuse frame discovery configuration | Equivalent upstream behavior passes the background rendering and timeout regressions |

Patch 01 is the only remaining downstream change and has no upstream
equivalent yet; it is the candidate for the next upstream PR.

## Retired patches

| Former patch | Replacement |
| --- | --- |
| `02-gateway-task-controls.patch` | Upstream PR #296: the optional UI channel `ui.task_preview` / `ui.task_focus` (renamed from `gateway.*`). WeKnora calls the official methods. |
| `03-vom-render-performance.patch` | Upstream PR #271 |
| `04-task-popup-ownership.patch` | Upstream PR #297: popups opened by native click/key input inside the Agent Window become observed, controllable tabs that session stop preserves. |
| `05-last-tab-lifecycle.patch` | Host side. `Manager.Call` runs `tool.tab_list` with `scope: "agent"` before `tool.tab_close`; when the target is the only tab in the Agent Window it first creates an agent-owned `about:blank` tab through `tool.tab_create`. Only official RPCs are involved. |
| `06-navigation-response-deadline.patch` | Upstream PR #291 (daemon navigation response grace). |

Background execution and viewport/full-page screenshot support use upstream
PRs #249, #250 and #253. Redirect document tracking and cancelled-navigation
reconciliation use upstream code including PR #280. Remote authentication,
credential storage/migration, renewal, connection settings and dedicated Agent
Windows use upstream PR #227. Do not restore the old
`remote-extension-connection.patch`, `shouldKeepTaskActive` callback or a second
focus-emulation cache.

Retained tasks keep the official session and debugger; turn completion only
stops preview polling in WeKnora. Completed tasks use native session stop.

## UI channel

The preview and focus side channel is now the official optional UI channel
documented in the upstream
[remote connection contract](https://github.com/Tencent/BrowserSkill/blob/c61eb7e4b1a5785d51b1a316dbf57686fecbf33d/docs/remote-extension-connection.md#optional-ui-channel).
Only authenticated remote sockets handle these request frames; they bypass the
native automation queue and never start a session:

```json
{"id":"wk-ui-unique","method":"ui.task_preview","params":{"session_id":"server-owned-session"}}
```

- `ui.task_preview`: returns `image_base64`, `format: "jpeg"`, `tab_id`,
  `title` and `captured_at`; the frame is at most 640 pixels wide, captures are
  coalesced per task and a poll that arrives while Chrome still holds one is
  refused with `timeout` / `preview_busy`. Overlays stay visible in previews.
- `ui.task_focus`: activates the task's owned tab and raises its window,
  returning `{ "focused": true }`.

Errors use the native envelope with typed codes (`not_found`, `timeout`,
`cancelled`, `cdp_failed`) and an optional `data.reason`. WeKnora maps every
error except `unknown_method` to a transient preview failure. Extensions built
before PR #296, including the published 0.3.0 ZIP, answer `unknown_method`;
WeKnora then disables preview polling and reports the extension as outdated.

## Intentional behavior changes versus the published 0.3.0 extension

- Remote tasks use official dedicated Agent Windows. No `tabGroups` permission.
- Popup attribution follows upstream PR #297: only a main-frame navigation
  target from a controlled source during native click/key input becomes an
  observed tab. Observed tabs are readable and closable but never enter the
  agent-created set; session stop preserves them and their window. Late or
  unattributed targets use the ordinary borrow flow, and an unowned tab that
  already sits in the Agent Window must be moved to a regular window before it
  can be borrowed.
- Closing the last tab of the Agent Window through `tab_close` keeps an
  agent-owned blank tab until session stop (host-side, see above), so Chrome's
  window removal is not misreported as a human interruption. Actual user window
  closure still pauses the task.
- Human help and borrowing follow the upstream focus/confirmation behavior.
- Remote upload/download remain unsupported, as defined upstream.

Even though the version remains 0.3.0, users must install the rebuilt ZIP.
Upgrade users by replacing the existing unpacked extension directory and
reloading it after ending active tasks. Keeping the extension ID preserves the
migrated credentials; installing under a new ID requires pairing again.

## Validation

Apply the patch to a clean pinned checkout, install frozen dependencies and run:

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
input RPC, agent popup selection/read/close, the in-window borrow rejection
followed by borrow approval/revocation of a tab moved to a regular window,
independent preview during help waits, focus, pause/resume, retained-session
continuity and completed-task cleanup. It separately verifies that agent
last-tab closure permits the next turn and manual window closure blocks
automation until explicit resume.

Upstream references: [PR #227](https://github.com/Tencent/BrowserSkill/pull/227),
[PR #291](https://github.com/Tencent/BrowserSkill/pull/291),
[PR #296](https://github.com/Tencent/BrowserSkill/pull/296),
[PR #297](https://github.com/Tencent/BrowserSkill/pull/297).
