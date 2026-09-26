# WeKnora plugin SDK (Go)

Build code plugins for WeKnora: web search providers, data source
connectors and document parsers that run as their own process. The module has no dependencies.

| Package | What it is |
| --- | --- |
| `pluginapi` | The extension protocol v1: envelope, errors, stream events, messages. `openapi.yaml` describes it for other languages. |
| `pluginsdk` | Write a plugin: register contributions, then `Serve`. |
| `client` | Call a plugin, as WeKnora does. |
| `conformance`, `cmd/weknora-plugin-conformance` | Check that a plugin speaks the protocol. |
| `pluginsign`, `cmd/weknora-plugin` | Sign a package and verify a signature. |

Writing Python? [`python/`](python/README.md) is the same SDK for Python
3.9+, with only the standard library.

## A connector in 30 lines

```go
package main

import (
	"context"
	"log"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

type notes struct{}

func (notes) Validate(ctx context.Context, call *pluginsdk.Call, cfg pluginsdk.ConnectorConfig) error {
	if cfg.Credentials["token"] == nil {
		return pluginapi.InvalidConfig("token is required", map[string]string{"credentials.token": "required"})
	}
	return nil
}

func (notes) ListResources(ctx context.Context, call *pluginsdk.Call, cfg pluginsdk.ConnectorConfig, parent string) ([]pluginapi.Resource, error) {
	return []pluginapi.Resource{{ExternalID: "inbox", Name: "Inbox"}}, nil
}

func (notes) Fetch(ctx context.Context, call *pluginsdk.Call, cfg pluginsdk.ConnectorConfig, in pluginapi.FetchInput, s *pluginsdk.Stream) (*pluginapi.Cursor, error) {
	if err := s.Item(pluginapi.FetchedItem{ExternalID: "n1", Title: "First note", Content: []byte("# Hello")}); err != nil {
		return nil, err
	}
	return &pluginapi.Cursor{State: map[string]any{"after": "n1"}}, nil
}

func main() {
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.notes", Version: "1.0.0"})
	p.Connector("notes", notes{})
	log.Fatal(p.Serve())
}
```

What the SDK does for you:

- **Handshake.** Started by WeKnora's plugin host, the plugin listens on the
  socket it is given and accepts only the host's token.
- **Remote mode.** Run on its own, it listens on `WEKNORA_PLUGIN_ADDR` and
  verifies signed requests with `WEKNORA_PLUGIN_SECRET`.
- **Per call.** Each call's deadline reaches your context. Returned
  `pluginapi` errors keep their code; anything else, and any panic, becomes
  `internal`.

Every call carries the tenant and the configuration, secrets already
decrypted, in three separate scopes:

- `call.Config.System`: set by the system administrator.
- `call.Config.Tenant`: set per workspace.
- the instance: `cfg`, or `call.DecodeInstance`.

Keep plugins stateless and never cache a tenant's credentials across calls.

## Parsers

`p.Parser(id, ...)` turns a document (`in.Content`, base64 on the wire) into
Markdown. WeKnora chunks the text and stores the images the Markdown
references: return them in `Images` with an `OriginalRef` matching the
`![](...)` target. Declare the extensions it handles in `plugin.yaml`
(`fileTypes: [srt, vtt]`). Return a retryable error (`unavailable`,
`rate_limited`) to have the document retried later; any other error fails
it for good.

## Chunkers

A chunker decides where a knowledge base's documents are cut. Workspaces
pick it as the chunking strategy (`plugin:acme.legal/clauses`). Return spans
of the text in characters (runes); WeKnora takes the text itself, and falls
back to its own strategies if the chunker fails.

```go
p.Chunker("clauses", pluginsdk.ChunkerFunc(
	func(ctx context.Context, call *pluginsdk.Call, in pluginapi.ChunkInput) ([]pluginapi.ChunkSpan, error) {
		return splitAtClauses([]rune(in.Text), in.ChunkSize), nil
	}))
```

```yaml
contributes:
  chunkers:
    - { id: clauses, name: { en-US: By clause } }
```

In Python, `@plugin.chunker("clauses")` returns `ChunkSpan`s or
`(start, end)` pairs; string indices are already characters.

## Pipeline hooks

A pipeline hook takes part in knowledge Q&A: `rewriteQuery` may replace the
question retrieval uses, `filterResults` may drop and reorder retrieved
passages (return the IDs to keep), `answer` may append Markdown after the
finished answer. WeKnora waits a few seconds at most and carries on without
a hook that fails.

```go
p.PipelineHook("guard", pluginsdk.PipelineHook{
	FilterResults: func(ctx context.Context, call *pluginsdk.Call,
		in pluginapi.FilterResultsInput) (pluginapi.FilterResultsOutput, error) {
		var keep []string
		for _, r := range in.Results {
			if !isConfidential(r.Content) {
				keep = append(keep, r.ID)
			}
		}
		return pluginapi.FilterResultsOutput{Keep: keep}, nil
	},
})
```

```yaml
contributes:
  pipelineHooks:
    - { id: guard, name: { en-US: Guard }, stages: [filterResults] }
```

In Python: `@plugin.hook("guard", "filterResults")` on `fn(call, input)`
returning `{"keep": [...]}`.

## IM channels

An IM channel brings a chat platform that calls back over HTTP. WeKnora
relays each request to the channel's callback URL to `Callback`, with the
channel's credentials in `call.Config.Instance`; return what the platform
should receive and the user's message, if there is one. WeKnora answers it
with the channel's agent and calls `Send` with the reply.

```go
p.IMChannel("zulip", zulipChannel{})

func (zulipChannel) Callback(ctx context.Context, call *pluginsdk.Call,
	req pluginapi.WebhookRequest) (pluginapi.IMCallbackOutput, error) {
	// verify req with call.Config.Instance["token"], then:
	return pluginapi.IMCallbackOutput{Message: &pluginapi.IMMessage{
		UserID: from, ChatID: stream, ChatType: "group", Content: text,
	}}, nil
}
```

```yaml
contributes:
  imChannels:
    - { id: zulip, name: { en-US: Zulip }, instanceSchema: schemas/channel.json }
```

In Python: `@plugin.im_channel("zulip")` on a class with
`callback(call, req)` and `send(call, message, content)`.

## Pages

A plugin can add pages to the app:
- `pages`: toolbox tabs.
- `settingsSections`: sections of the settings dialog.
- `kbTabs`: tabs on each knowledge base.

Each is an HTML `entry` under `ui/` in the package. It runs in a sandboxed
iframe and talks to the app through
[`@weknora/plugin-ui`](../packages/plugin-ui). A page's requests reach the
plugin's `UI` handler:

```go
p.UI(func(ctx context.Context, call *pluginsdk.Call, req pluginapi.UIRequest) (*pluginapi.UIResponse, error) {
	// req.Mount ("pages/links"), req.Method, req.Path, req.Body, req.Role
	return pluginsdk.UIJSON(200, links)
})
```

WeKnora refuses a caller below the page's `minRole` before the call.
The handler can check `req.Role` for anything finer.

**Instance editors.** A connector or web search contribution can name an
`editor` page (`editor: ui/editor.html`) for settings a schema form cannot
express. It shows below the generated form:
- `context.context.values` holds the instance, in the shape the plugin's
  `instanceSchema` has (connectors: `{credentials, settings}`).
- `wk.form.set(values)` merges values into the form; the `values` event
  reports what the user changes.
- Its requests use the mount `connectors/<id>` or `webSearch/<id>` and need
  an admin.

## Events

List the events a plugin wants in `permissions.events`. The administrator
sees them at install.

| Event | When | Data |
| --- | --- | --- |
| `knowledge.ingested` | A document finished processing | `KnowledgeEventData` |
| `knowledge.failed` | A document failed for good | `KnowledgeEventData` with `error` |
| `knowledge.deleted` | A document was deleted | `KnowledgeEventData` |
| `chat.answered` | An answer completed | `ChatEventData`, including the question and answer |

```go
p.OnEvent(func(ctx context.Context, call *pluginsdk.Call, ev pluginapi.EventDelivery) error {
	// ev.ID stays the same across retries: deduplicate on it.
	return nil
})
```

Delivery is at least once, in the background:
- A workspace receives events only while it has the plugin switched on.
- A retryable error (`unavailable`, `rate_limited`) or a timeout brings
  the event back later with the same `ID` and a higher `Attempt`.
- Any other error drops the event.
- Ignore types you do not know: the list only grows.

## Webhooks

`contributes.webhooks` gives each workspace a secret URL per webhook,
under `/api/v1/plugin-callbacks/`. Workspace admins copy it from the plugin
center, and it is in `call.Context.Webhooks` when WeKnora knows its
public address.

```go
p.Webhook("events", func(ctx context.Context, call *pluginsdk.Call, req pluginapi.WebhookRequest) (*pluginapi.WebhookResponse, error) {
	// call is the workspace the URL belongs to; verify the sender with its configuration.
	return &pluginapi.WebhookResponse{Status: 204}, nil
})
```

WeKnora answers 404 unless all of these hold:
- the URL is genuine;
- the webhook exists;
- the workspace has the plugin on.

Bodies are limited to 1 MB, with 20 calls a second per URL.

## Agent tools

Agent tools are MCP tools. A plugin with code can serve them itself: declare
an `mcpServers` contribution without `mcp.url`, then add the tools.

```yaml
contributes:
  mcpServers:
    - id: tools
      name: { en-US: ACME Issues }
      toolViews:                 # optional: how the chat shows structured results
        search_issues:
          view: table
          items: issues
          columns: [{ field: key, link: url }, summary, status]
```

```go
p.Tool("tools", pluginapi.Tool{
	Name:        "search_issues",
	Description: "Search issues",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
}, func(ctx context.Context, call *pluginsdk.Call, args json.RawMessage) (*pluginapi.ToolResult, error) {
	// call.Config.Tenant is the workspace's plugin configuration.
	return pluginapi.StructuredResult(map[string]any{"issues": rows}, "3 issues ..."), nil
})
```

How it works:
- **Discovery.** Every workspace that switches the plugin on gets the server
  as a read-only MCP service. Agents use it like any MCP service, with the
  same per-tool approval settings.
- **Transport.** Messages reach the plugin at `/v1/mcp/{id}` inside the usual
  envelope. Each call therefore carries the workspace's context and
  configuration (OAuth fields already swapped for tokens), and the server
  keeps no session.
- **Results.** The model reads the text content. Return failures the model
  should see as `pluginapi.ToolError` (or any error); they become results with
  `isError`.
- **`toolViews`** map a tool's `structuredContent` to a view, so the chat can
  show it without plugin code:

| View | Shows | Fields |
| --- | --- | --- |
| `table` | A list as rows | `items`, `columns` (field, title, link) |
| `cards` | A list as cards | `items`, `title`, `subtitle`, `body`, `link` |
| `kv` | One object's fields | `items`, `title`, `link`, `fields` |
| `markdown` | Rendered Markdown | `field` (the text content when empty) |
| `json` | The data, formatted | - |
| `page` | A page of the plugin (`entry` under `ui/`) | Gets `{tool, arguments, result}` as its context |

  Paths are dotted field paths into the structured content. A page's
  requests use the mount `mcpServers/<id>`.

A plugin can also point `mcp.url` at an MCP server it runs elsewhere; then
headers carry the configuration (`${config.<key>}`), and `toolViews` apply
too.

## Dynamic choices and OAuth

Two schema keywords let a form ask the plugin while someone fills it in.

**`x-options`** loads a field's choices from the plugin, for lists that
depend on the account (projects, spaces, channels). It works on a string or
an array of strings:

```yaml
project:
  type: string
  x-options: { name: projects, dependsOn: [site, token], search: true }
```

```go
p.Options("projects", func(ctx context.Context, call *pluginsdk.Call, in pluginapi.OptionsInput) ([]pluginapi.Option, error) {
	// call.Config holds what the form holds now, secrets included.
	if call.Config.Tenant["token"] == nil {
		return nil, pluginapi.InvalidConfig("enter the token first", map[string]string{"token": "required"})
	}
	return []pluginapi.Option{{Value: "p1", Label: "Project " + in.Query}}, nil
})
```

- `in.Scope` is `system`, `tenant` or `instance`; the values are in the
  matching part of `call.Config`.
- Secrets the form shows redacted are filled in from the stored
  configuration.
- The form reloads the list when a `dependsOn` field changes. With
  `search`, `in.Query` is what the user typed.
- WeKnora does not check a saved value against the list; the plugin
  should.

**`x-oauth`** turns a string field into an account connection. WeKnora runs
the OAuth 2.0 authorization code flow and keeps the tokens:

```yaml
config:
  system: schemas/system.yaml     # client_id, client_secret (x-secret)
# schemas/tenant.yaml
account:
  type: string
  x-oauth:
    authorizeUrl: https://auth.example.com/oauth/authorize
    tokenUrl: https://auth.example.com/oauth/token
    scopes: [read]
    clientId: ${system.client_id}
    clientSecret: ${system.client_secret}
    pkce: true
    params: { audience: api.example.com }   # extra authorize parameters
```

- The form shows a connect button that opens the provider's consent page.
- The field stores `oauth:<connection>`. At call time the plugin receives a
  fresh access token in its place; WeKnora refreshes it when it is about to
  expire. A connection that can no longer be refreshed arrives as `""`, so
  answer `unauthorized` then.
- The platform registers the OAuth app with the provider, with the
  redirect URI `<WeKnora address>/api/v1/plugin-oauth/callback`, and enters
  its credentials in the plugin's platform configuration.
- `tokenUrl` must be https. WeKnora calls it itself, so it needs no
  `permissions.egress` entry.

## Calling back into WeKnora (Host API)

A plugin that declares `permissions.hostApi` gets a short-lived token with
each call; `call.Host()` returns a client for it, or nil without a grant.
Scopes:

- `kv`: a key-value store of the plugin, separate per workspace, so the
  plugin needs no database.
  - `KVGet`, `KVPut` (optional TTL), `KVDelete` and `KVList`.
  - Keys up to 256 bytes, JSON values up to 64 KB, 10,000 keys per
    workspace.
- `datasources`: the workspace's data sources of the plugin's own
  connectors.
  - `DataSources` lists them with their selected resources.
  - `SyncDataSource` starts an incremental sync, unless one is already
    running (answering `running`).
  - Pair it with a webhook so changes arrive at once instead of at the
    next scheduled sync; the Jira example does.

The token is valid for a few minutes: use `call.Host()` within the call and
don't keep it.

## Packaging

A package is a zip (`.wkp`) with `plugin.yaml` at its root:

```yaml
schemaVersion: 1
id: acme.notes
version: 1.0.0
apiVersion: weknora.plugin/v1
name: { en-US: ACME Notes }
publisher: { id: acme }
runtime: { type: host, kind: binary, entry: "bin/{os}-{arch}/notes" }
permissions:
  egress: [api.acme.example]      # hosts the plugin may reach; "*" = any public host
contributes:
  connectors:
    - id: notes
      name: { en-US: ACME Notes }
      instanceSchema: schemas/notes.yaml   # fields with x-group: settings are settings, the rest credentials
```

A few rules the manifest and host enforce:

- **Binaries.** Build one per platform into `bin/<os>-<arch>/`; WeKnora
  runs the build for its own OS and architecture.
- **Python.** A plugin can instead be Python source:
  - Declare `runtime: { type: host, kind: python, entry: main.py }`.
  - WeKnora runs the entry with its own `python3` (or
    `WEKNORA_PLUGIN_PYTHON`).
  - `vendor/` and the package root are on `PYTHONPATH`.
- **Environment.** The process gets no environment from WeKnora beyond the
  `WEKNORA_PLUGIN_*` variables.
- **Outbound traffic.** It goes through the host's egress proxy, which
  forwards only to the hosts in `permissions.egress` and never to private
  addresses.
- **Resources.** `runtime.resources: { cpu: "500m", memory: 512Mi }`
  caps the process (Linux). Memory is always capped. CPU is capped only
  when the platform delegates a cgroup to WeKnora
  (`WEKNORA_PLUGIN_CGROUP`).
- **IDs.** Contribution IDs are qualified with the plugin ID
  (`acme.notes/notes`); for connectors and web search that must stay within
  50 characters.

Complete plugins with their `package.sh`:

- `examples/plugins/rss`: a connector.
- `examples/plugins/subtitles`: a parser using the Host API.
- `examples/plugins/notebooks`: a parser written in Python.
- `examples/plugins/links`: pages (toolbox, settings, knowledge base tab), in Python.
- `examples/plugins/activity`: events, a webhook and a page, in Python.
- `examples/plugins/jira`: a Jira Cloud connector with an OAuth field, dynamic
  options, incremental sync with deletions; agent tools with result views; and
  a skill.

## Signing

A signed package tells WeKnora who published it. A platform that lists your
public key in its trust store shows the package as verified (or official)
and, if it only accepts verified plugins, lets it install at all. An unsigned
package is a community package.

```bash
go install github.com/Tencent/WeKnora/pluginsdk/cmd/weknora-plugin@latest
weknora-plugin keygen -out acme.key     # prints the public key: ed25519:...
weknora-plugin sign -key acme.key -key-id acme-2026 acme-search-1.0.0.wkp
weknora-plugin verify -pubkey ed25519:... acme-search-1.0.0.wkp
```

`sign` adds `plugin.sig` to the package. The signature covers every other
file, so sign last and publish the digest of the signed package. Keep the
private key out of the repository.

## Testing

```bash
go run github.com/Tencent/WeKnora/pluginsdk/cmd/weknora-plugin-conformance -url http://localhost:8080 -secret $SECRET
```

In Go tests, run `conformance.Run` against `httptest.NewServer(p.Handler())`.

## Installing

In WeKnora: **System administration → Plugin management → Install plugin**.
Upload the `.wkp` or give its URL, review what it adds and reaches, and
install. Each workspace then enables it under **Settings → Plugins**.

## Running as a remote service

A plugin can also run as a service you deploy yourself, for instance in its
own container or on another team's cluster. Its package then carries only
`plugin.yaml` (plus schemas):

```yaml
runtime: { type: remote }
```

1. **Install.** Give the service URL when installing. WeKnora checks the
   service's `/v1/manifest` against the package: same ID, version and
   contributions.
2. **Keep the secret.** Installing shows a signing secret once. Start the
   service with it as `WEKNORA_PLUGIN_SECRET` (and `WEKNORA_PLUGIN_ADDR`,
   default `:8080`). The SDK rejects requests without a valid signature.
3. **Private hosts.** A service on a private network must be listed in
   WeKnora's `SSRF_WHITELIST`.
4. **Host API.** Remote plugins get a Host API token only when
   `WEKNORA_PLUGIN_HOST_API_URL` tells WeKnora its address as the service
   sees it.

The plugin detail page changes the URL and rotates the secret. After a
rotation, calls fail until the service has the new secret.

Upgrade the service and the package together. While the service reports a
version other than the active package, calls are refused and the plugin
shows as degraded.
