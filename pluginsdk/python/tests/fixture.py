"""A plugin exercising every contribution kind: the SDK's tests and the Go
conformance suite run it."""

from __future__ import annotations

import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from weknora_plugin import (  # noqa: E402
    ChunkSpan,
    Cursor,
    ErrorCode,
    FetchedItem,
    ParsedImage,
    ParseOutput,
    Plugin,
    PluginError,
    Resource,
    SearchResult,
    UIResponse,
    WebhookResponse,
    invalid_config,
)

seen_events = []

plugin = Plugin("acme.fixture", "1.0.0")


@plugin.config_validator
def validate(call):
    if call.tenant.get("region") == "mars":
        raise invalid_config("unknown region", {"region": "no such region"})


@plugin.web_search("echo")
def search(call, q):
    if q.query == "down":
        raise PluginError(ErrorCode.UNAVAILABLE, "upstream is down")
    if q.query == "crash":
        raise RuntimeError("boom")
    if q.query == "slow":
        time.sleep(0.5)
    return [SearchResult(title=q.query, url=f"https://example.com/?tenant={call.tenant_id}", snippet=call.locale)]


@plugin.parser("upper")
def parse(call, doc):
    text = doc.content.decode("utf-8", "replace")
    return ParseOutput(
        markdown=text.upper() + "\n\n![](img/dot.png)",
        images=[ParsedImage(original_ref="img/dot.png", data=b"\x89PNG", mime_type="image/png")],
        metadata={"fileType": doc.file_type},
    )


@plugin.chunker("paragraphs")
def paragraphs(call, inp):
    if inp.text == "out of range":
        return [(0, 100)]
    spans, start = [], 0
    for part in inp.text.split("\n\n"):
        spans.append(ChunkSpan(start=start, end=start + len(part), context_header=f"size {inp.chunk_size}"))
        start += len(part) + 2
    return spans


@plugin.connector("notes")
class Notes:
    def validate(self, call, cfg):
        if cfg.credentials.get("token") == "bad":
            raise PluginError(ErrorCode.UNAUTHORIZED, "token rejected")

    def list_resources(self, call, cfg, parent_id):
        return [Resource(external_id="inbox", name="Inbox", has_children=not parent_id)]

    def fetch(self, call, cfg, inp, stream):
        start = int((inp.cursor.state or {}).get("after", 0)) if inp.cursor else 0
        if cfg.settings.get("fail") == "before":
            raise PluginError(ErrorCode.UNAUTHORIZED, "token rejected")
        for i in range(start, start + 3):
            stream.item(FetchedItem(external_id=f"n{i}", title=f"Note {i}", content=f"# Note {i}"))
            if cfg.settings.get("fail") == "during":
                raise PluginError(ErrorCode.RATE_LIMITED, "slow down", retry_after=7)
        stream.checkpoint(Cursor(state={"after": start + 3}))
        stream.progress("done")
        return Cursor(state={"after": start + 3})


@plugin.on_event
def on_event(call, ev):
    if ev.type == "knowledge.failed":
        raise PluginError(ErrorCode.UNAVAILABLE, "try later")
    seen_events.append((ev.id, ev.type, ev.attempt, call.tenant_id, (ev.data or {}).get("knowledgeId")))


@plugin.tool("issues", "search", "Search issues", {"type": "object", "properties": {"q": {"type": "string"}}}, read_only=True)
def search_issues(call, args):
    if args.get("q") == "boom":
        raise PluginError(ErrorCode.UNAUTHORIZED, "token expired")
    if args.get("q") == "text":
        return "plain"
    return {"q": args.get("q"), "tenant": call.tenant_id}


@plugin.options("projects")
def projects(call, inp):
    if not call.tenant.get("token"):
        raise invalid_config("enter a token first", {"token": "required"})
    return [("p1", f"Project {inp.query}"), ("p2", "Other")]


@plugin.webhook("inbox")
def inbox(call, req):
    if req.headers.get("X-Signature") != "ok":
        return WebhookResponse(status=401, body="bad signature")
    return {"path": req.path, "got": req.json(), "tenant": call.tenant_id}


@plugin.ui
def ui(call, req):
    if req.path == "/missing":
        return UIResponse(status=404, body={"error": "no such thing"})
    return {"mount": req.mount, "method": req.method, "path": req.path, "role": req.role, "body": req.body}


if __name__ == "__main__":
    plugin.serve()
