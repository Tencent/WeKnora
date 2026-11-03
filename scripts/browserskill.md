# BrowserSkill upstream integration

The extension and daemon are based on the official `ext-v0.4.1` and
`cli-v0.4.1` tags, both at commit
`b8f2d4f0fa35c8c7c866229e1cf3b3329ae39de5`. Both report version `0.4.1`
and protocol `1.8`; the exact source baseline is recorded in
`scripts/browserskill-release.json`. The daemon is built with
`cargo build --locked --release -p bsk` from the same commit as the extension.
Native builds require Rust/Cargo and a C compiler (plus CMake on Linux).
Docker builds on the target architecture.

The 0.4.1 extension requires daemon protocol 1.6 or later, so the previous
0.3.1 daemon (protocol 1.3) cannot connect. Target-policy actions require
protocol 1.8 on both peers. End active tasks, rebuild/redeploy the app and
restart it to replace its running daemon, then update the frontend and extension.
Updating a binary on disk alone does not replace a running daemon.

## Shared control

Upgrading the daemon alone is insufficient: new sessions enable shared control
and reject page actions without an explicit observation/control receipt.
WeKnora exposes the official `tool.cooperate` RPC as `local_browser` method
`cooperate`, with `action:sync`, `action:status` and `action:wait`.

- After the initial blank-tab navigation, call `cooperate` with `action:sync`
  and inspect its `observation` or `changes` before choosing an action.
- Sync defaults to `control_policy:auto`, selecting `target` for a new session
  and retaining the negotiated policy thereafter. Pass the returned `context_id`
  on page actions. DOM-targeted actions can reuse it across actions and reads.
- Scripts, subsequent navigation and untargeted actions also need the sync's
  single-use `observation_id`. Synchronize and inspect again before the next
  such action. Passive `observe`/`snapshot` do not replace synchronization.
- Preserve caller-supplied `expect` conditions literally. `depends_on` is only
  available with `control_policy:strict`, whose contexts authorize one action.
- Human input and navigation revoke control. Wait for hand-back, synchronize and
  reconsider the live result. WeKnora never silently obtains a fresh receipt or
  retries a rejected action. A stopped/held BrowserSkill overlay requires the
  user to return control in the browser before resuming in WeKnora. If a browser
  stop hid its toolbar, clicking Continue in WeKnora restores it with a passive
  observe and keeps the task paused until the browser returns control.
- Pages with human participation survive session stop. Moving a controlled tab
  out of its Agent Window ends its upstream session and preserves the pages.

`scripts/build_browserskill.sh` builds the unmodified upstream checkout. No
BrowserSkill patches are applied or maintained downstream.

Renderer read reliability is provided by upstream PR #318: snapshot/AX captures
have a 20 s deadline, other bounded reads use 10 s, and a pending timed-out read
blocks further guarded reads until it settles or its debugger session detaches.
Navigation alone does not clear the gate. Errors retain
`data.reason = renderer_read_timeout`; unavailable child frames can be omitted
without discarding healthy frame content.

## Retired patches

| Former patch | Replacement |
| --- | --- |
| `01-browser-read-reliability.patch` | Upstream PR #318: bounded renderer reads, per-session read gates, capture fallback termination and debugger frame discovery. |
| `02-gateway-task-controls.patch` | Upstream PR #296: the optional UI channel `ui.task_preview` / `ui.task_focus` (renamed from `gateway.*`). WeKnora calls the official methods. |
| `03-vom-render-performance.patch` | Upstream PR #271 |
| `04-task-popup-ownership.patch` | Upstream PR #297: popups opened by native click/key input inside the Agent Window become observed, controllable tabs that session stop preserves. |
| `05-last-tab-lifecycle.patch` | Host preflight. `Manager.Call` checks `tool.tab_list` with `scope: "agent"` before `tool.tab_close`. Closing the final tab requires an explicit `tab_create` of `about:blank`, then sync of the original tab. Automatic placeholder creation would invalidate the caller's context. |
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
[remote connection contract](https://github.com/Tencent/BrowserSkill/blob/b8f2d4f0fa35c8c7c866229e1cf3b3329ae39de5/docs/remote-extension-connection.md#optional-ui-channel).
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
before PR #296 answer `unknown_method`;
WeKnora then disables preview polling and reports the extension as outdated.

## Behavior of the 0.4.1 extension

- Remote tasks use official dedicated Agent Windows. No `tabGroups` permission.
- Popup attribution follows upstream PR #297: only a main-frame navigation
  target from a controlled source during native click/key input, within the
  same Agent Window, becomes an observed tab. Observed tabs are readable and
  closable but never enter the agent-created set; session stop preserves them and their window. Independent
  popup windows, late or unattributed targets use `tab_borrow` and `tab_return`.
  Unrelated user-created tabs are routed out of the Agent Window. An unowned tab
  that remains inside must be moved to a regular window before it can be borrowed.
- Closing the last tab of the Agent Window requires the caller to create an
  agent-owned `about:blank` tab first, then synchronize the original target
  before closing it. The host refuses an unprepared close without changing tabs
  or refreshing caller evidence. The blank tab survives until session stop,
  preventing normal cleanup from being mistaken for a human window closure.
  Actual user window closure still pauses the task.
- Borrow confirmation follows the browser's preference and uses an HTTP(S) page
  in a regular user window. The integration fixture supplies such a page; neither
  extension settings nor a popup window can host the prompt. Borrowed tabs cannot
  be closed through `tab_close`. Return them with `tab_return`; if the original
  window disappeared, upstream may create a normal fallback window with a new-tab
  placeholder. These returned user pages are preserved when the task ends.
- Human help follows the upstream focus/confirmation behavior.
- Remote upload/download remain unsupported, as defined upstream.

Users need extension 0.4.1 or later, from the Chrome Web Store, Edge Add-ons or
the bundled ZIP. Upgrade unpacked installs by replacing the existing extension
directory and reloading it after ending active tasks. Keeping the extension ID preserves the
migrated credentials; installing under a new ID requires pairing again.

## Validation

Use a clean pinned upstream checkout, install frozen dependencies and run:

```sh
pnpm --filter @browser-skill/extension exec wxt prepare
pnpm --filter @browser-skill/extension compile
pnpm --filter @browser-skill/extension test
pnpm --filter @browser-skill/vom test
pnpm ext:build:zip
BSK_GEOMETRY_CHROME=/path/to/test-chrome BSK_BACKGROUND_CHROME=/path/to/test-chrome \
  pnpm --filter @browser-skill/extension exec vitest run --maxWorkers=1 \
  src/tools/__tests__/background-execution.browser.test.ts \
  src/tools/__tests__/background-screenshot.browser.test.ts \
  src/tools/__tests__/background-full-page.browser.test.ts
cargo test --locked -p bsk daemon::ipc::tests
```

Run WeKnora's `TestRealExtension` with the built extension, pinned daemon and an
isolated Chromium profile. It exercises actual pairing, explicit shared-control
receipts, context reuse, rejected expectations, screenshot and input RPC,
same-window target selection/read/close, independent popup borrow/read/return and
access revocation, unrelated user-tab routing followed by borrow approval/revocation,
independent preview during help waits, focus, pause/resume, retained-session
continuity and completed-task cleanup. It separately verifies that agent
last-tab closure permits the next turn and manual window closure or moving a
controlled tab out blocks automation until explicit resume. Cleanup checks
preserve the exact initial, returned, human-participated and moved-out tab IDs,
plus the new-tab placeholder recorded from an official fallback return;
arbitrary extra tabs still fail the test.

```sh
BROWSERSKILL_TEST_EXTENSION=/path/to/unpacked/chrome-mv3 \
BROWSERSKILL_TEST_BINARY=/path/to/bsk \
BROWSERSKILL_TEST_CHROMIUM=/path/to/test-chromium \
BROWSERSKILL_TEST_PLAYWRIGHT=/path/to/playwright-core/index.mjs \
  go test ./internal/browserskill -run '^TestRealExtension$' -count=1 -v
```

Upstream references: [PR #227](https://github.com/Tencent/BrowserSkill/pull/227),
[PR #291](https://github.com/Tencent/BrowserSkill/pull/291),
[PR #296](https://github.com/Tencent/BrowserSkill/pull/296),
[PR #297](https://github.com/Tencent/BrowserSkill/pull/297),
[PR #318](https://github.com/Tencent/BrowserSkill/pull/318).
