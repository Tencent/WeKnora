# @weknora/plugin-ui

The page side of the bridge between a WeKnora plugin's pages and the app.
It has no dependencies and needs no build step.

WeKnora shows a plugin page in a sandboxed iframe:
- The page has an opaque origin, so it cannot read the app's storage.
- A CSP (`connect-src 'none'`) stops it reaching the network.

Everything else goes through this bridge.

```js
import { connect } from './weknora-plugin-ui.js'

const wk = await connect()
wk.context.mount        // "pages/links"
wk.context.context      // what the mount passes, e.g. { knowledgeBaseId }
const { body } = await wk.get('/links')        // the plugin's UI handler answers
await wk.put('/links', [...body, { url }])
await wk.toast('Saved', 'success')
if (await wk.confirm('Remove it?')) { /* … */ }
wk.navigate('/platform/knowledge-bases')
```

Ship `index.js` inside the plugin package, for example as
`ui/weknora-plugin-ui.js`, and load your page script with
`<script type="module">`.

## What a page can do

| Call | Does |
| --- | --- |
| `request(method, path, body)`, `get`, `post`, `put`, `delete` | Calls the plugin's own backend: the `UI` handler of the Go or Python SDK. Resolves `{status, body}`; non-2xx rejects unless `{ throwOnError: false }`. |
| `toast(message, theme)` | A toast in the app: info, success, warning or error. |
| `confirm(message, title?)` | A confirmation dialog; resolves true or false. |
| `navigate(path)` | Opens a page of the app. |
| `resize(height)` | Sets the frame height. `connect()` does this automatically unless `autoResize: false`: it reports the content's height (the bottom of `<body>` and its children, with margins), so the frame grows and shrinks with the page. Keep `<body>` at its content's height (no `height: 100vh`), or the frame cannot shrink. |
| `close()` | Closes the page, where the mount allows it. |
| `on('theme' \| 'locale' \| 'init', fn)` | Theme or language changed, or the mount's context changed (another knowledge base). |

`connect()` applies the app's theme:
- `data-theme="light|dark"` on `<html>`.
- Design tokens as CSS variables, such as `--wk-brand-color`,
  `--wk-text-color-primary`, `--wk-bg-color-container` and
  `--wk-component-border`.

## Looking like the app

`kit.css` gives a page the app's look without a framework. Ship it next to
the bridge, for example as `ui/weknora-plugin-ui.css`, and link it before
your own stylesheet:

```html
<link rel="stylesheet" href="weknora-plugin-ui.css" />
```

It reads the `--wk-*` tokens `connect()` sets, so pages follow the app's
colours and dark mode. The primitives:

| Class | For |
| --- | --- |
| `wk-list`, `wk-row` (`__main`, `__title`, `__sub`, `__meta`, `__actions`) | A bordered list of rows, the shape of most pages |
| `wk-avatar` | A letter or icon at the start of a row |
| `wk-btn` (`--primary`, `--text`, `--danger`, `--sm`) | Buttons |
| `wk-input` | Text inputs |
| `wk-tag` (`--brand`, `--success`, `--warning`, `--danger`) | Status labels |
| `wk-seg` | A segmented filter; mark the active button `aria-pressed="true"` |
| `wk-toolbar` (`--end`, `--below`) | A row of controls above or below a list |
| `wk-empty`, `wk-muted` | Empty states and secondary text |

The app draws the page's title and surface. In a settings section it shows
the section name, its description and which plugin provides it, so the page
starts with its content, not a heading.

## Where pages appear

Declare pages in `plugin.yaml` with an `entry` under `ui/`:

| Point | Shown | Default `minRole` |
| --- | --- | --- |
| `pages` | A tab of the toolbox | viewer |
| `settingsSections` | A section of the settings dialog, under Plugins | admin |
| `kbTabs` | A tab on each knowledge base; `context.knowledgeBaseId` | viewer |

WeKnora checks `minRole` again on each request a page makes. It also
passes the caller's role to the plugin, so the backend can refuse
anything finer-grained.

See `examples/plugins/links` for a plugin with all three.
