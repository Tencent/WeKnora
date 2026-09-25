# WeKnora plugin SDK (Go)

Build code plugins for WeKnora: web search providers, data source
connectors and document parsers that run as their own process. The module has no dependencies.

| Package | What it is |
| --- | --- |
| `pluginapi` | The extension protocol v1: envelope, errors, stream events, messages. `openapi.yaml` describes it for other languages. |
| `pluginsdk` | Write a plugin: register contributions, then `Serve`. |
| `client` | Call a plugin, as WeKnora does. |
| `conformance`, `cmd/weknora-plugin-conformance` | Check that a plugin speaks the protocol. |

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

## Calling back into WeKnora (Host API)

A plugin that declares `permissions.hostApi` gets a short-lived token with
each call; `call.Host()` returns a client for it, or nil without a grant.
Scopes:

- `kv`: a key-value store of the plugin, separate per workspace, so the
  plugin needs no database.
  - `KVGet`, `KVPut` (optional TTL), `KVDelete` and `KVList`.
  - Keys up to 256 bytes, JSON values up to 64 KB, 10,000 keys per
    workspace.

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
- **Environment.** The process gets no environment from WeKnora beyond the
  `WEKNORA_PLUGIN_*` variables.
- **Outbound traffic.** It goes through the host's egress proxy, which
  forwards only to the hosts in `permissions.egress` and never to private
  addresses.
- **IDs.** Contribution IDs are qualified with the plugin ID
  (`acme.notes/notes`); for connectors and web search that must stay within
  50 characters.

See `examples/plugins/rss` (a connector) and `examples/plugins/subtitles` (a
parser using the Host API) for complete plugins with their `package.sh`.

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
