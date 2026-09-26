# weknora-plugin

Build [WeKnora](https://github.com/Tencent/WeKnora) code plugins in Python.
The package speaks the same extension protocol as the Go SDK. It uses only
the standard library and supports Python 3.9 and later.

```python
from weknora_plugin import Plugin, SearchResult

plugin = Plugin("acme.search", "1.0.0")  # must match plugin.yaml


@plugin.web_search("web")
def search(call, q):
    # call.tenant_id, call.locale, call.system / call.tenant / call.instance
    return [SearchResult(title=q.query, url="https://example.com")]


if __name__ == "__main__":
    plugin.serve()
```

## Contributions

**Web search.**

```python
@plugin.web_search(id)
def search(call, q: SearchInput) -> list[SearchResult]: ...
```

**Parser.** Return a `ParseOutput`, or just the Markdown as a `str`.

```python
@plugin.parser(id)
def parse(call, doc: ParseInput) -> ParseOutput: ...
```

- `doc.content` is the file as bytes.
- Images the Markdown references as `![](ref)` go in `images` with a
  matching `original_ref`.

**Connector.** A class, registered with `@plugin.connector(id)` (or pass
an instance):

```python
@plugin.connector("notes")
class Notes:
    def validate(self, call, cfg: ConnectorConfig) -> None: ...
    def list_resources(self, call, cfg, parent_id: str) -> list[Resource]: ...
    def fetch(self, call, cfg, inp: FetchInput, stream: Stream) -> Cursor | None:
        for note in changed_since(inp.cursor):
            stream.item(FetchedItem(external_id=note.id, title=note.title, content=note.markdown))
        stream.checkpoint(Cursor(state={"after": last_id}))  # at page boundaries
        return Cursor(state={"after": last_id})
    # optional: resolve_ancestors(self, call, cfg, resource_ids) -> list[str]
```

**Configuration check.**

```python
@plugin.config_validator
def validate(call) -> None: ...
```

**Pages.** Requests from the plugin's pages (`pages`,
`settingsSections`, `kbTabs` in plugin.yaml) arrive at one handler. Return a
`UIResponse`, or any JSON value for a 200:

```python
@plugin.ui
def ui(call, req: UIRequest):
    # req.mount ("pages/links"), req.method, req.path, req.body, req.role
    return UIResponse(status=200, body=links)
```

The pages themselves are HTML under `ui/` that use
[`@weknora/plugin-ui`](../../packages/plugin-ui).

## Errors

Raise `PluginError(ErrorCode.X, "message")` to choose what WeKnora sees:

- `unauthorized`: credentials stopped working.
- `rate_limited` (with `retry_after=`): back off.
- `unavailable`: retry later.
- `invalid_config(message, {"settings.url": "required"})`: point at form
  fields.

Any other exception becomes `internal`, and its traceback goes to stderr.

## Calling back into WeKnora

A plugin granted Host API scopes (`permissions.hostApi` in plugin.yaml) gets
a client per call from `call.host()`. It returns `None` without a grant.

```python
host = call.host()
host.kv_put("cursor", {"page": 3}, ttl=3600)
host.kv_get("cursor", default={})
```

The token behind it lasts a few minutes: use it within the call.

## Running

`plugin.serve()` picks its mode from the environment.

- **Host mode.** The WeKnora plugin host starts the plugin with
  `WEKNORA_PLUGIN_SOCKET` and `WEKNORA_PLUGIN_TOKEN`. The plugin listens
  on that socket and prints the handshake on stdout. Keep stdout free of
  anything else, and log to stderr (`plugin.logger`).
- **Remote mode.** Run on its own, it listens on `WEKNORA_PLUGIN_ADDR`
  (default `:8080`). It accepts only requests signed with
  `WEKNORA_PLUGIN_SECRET`, the secret shown when the plugin was registered.

On SIGTERM it stops accepting calls and lets the ones in flight finish.
Each thread handles one call. `call.deadline` says when WeKnora stops
waiting.

## Packaging for the plugin host

```yaml
runtime: { type: host, kind: python, entry: main.py }
```

WeKnora runs the entry with its own `python3`. It puts the package's
`vendor/` directory and the package root on `PYTHONPATH`. Vendor the SDK
and any dependencies:

```bash
pip install --target vendor weknora-plugin   # or copy src/weknora_plugin
```

Pure-Python dependencies work everywhere. Ones with native code must match
the server's platform. For a complete plugin, see
`examples/plugins/notebooks` (a Jupyter notebook parser).

## Testing

`plugin.test_server()` serves the plugin on a loopback port without
authentication, for unit tests. To check the protocol end to end, run the
conformance suite from the Go SDK against a running plugin:

```bash
go run github.com/Tencent/WeKnora/pluginsdk/cmd/weknora-plugin-conformance -url http://localhost:8080 -secret $SECRET
```
