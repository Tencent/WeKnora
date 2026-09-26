"""A WeKnora plugin that keeps an activity feed of the workspace, written
with the Python SDK. It shows the three ways data reaches a plugin without a
user asking: events WeKnora sends (at least once, so they are deduplicated
by ID), a webhook other systems call, and a page that reads the feed."""

from __future__ import annotations

import hmac
from datetime import datetime, timezone

from weknora_plugin import ErrorCode, Plugin, PluginError, UIResponse, WebhookResponse

plugin = Plugin("weknora-examples.activity", "1.0.0")

FEED_KEY = "feed"
MAX_ENTRIES = 100


def store(call):
    host = call.host()
    if host is None:
        # Retryable: the Host API may just be unreachable for a moment.
        raise PluginError(ErrorCode.UNAVAILABLE, "the Host API is not available to this plugin")
    return host


def describe(ev, include_answers: bool) -> dict | None:
    d = ev.data or {}
    title = d.get("title") or d.get("fileName") or d.get("knowledgeId", "")
    if ev.type == "knowledge.ingested":
        return {"kind": "ingested", "text": title}
    if ev.type == "knowledge.failed":
        return {"kind": "failed", "text": f"{title}: {d.get('error', '')}".strip(": ")}
    if ev.type == "knowledge.deleted":
        return {"kind": "deleted", "text": title}
    if ev.type == "chat.answered":
        text = (d.get("question") or "").strip()[:200]
        if include_answers:
            first = (d.get("answer") or "").strip().splitlines()
            text += f" → {first[0][:200]}" if first else ""
        return {"kind": "answered", "text": text}
    return None  # a type added after this plugin was written


def append(host, entry: dict) -> None:
    feed = host.kv_get(FEED_KEY, [])
    # Deliveries repeat after a failure; the event ID makes them harmless.
    if any(e.get("id") == entry["id"] for e in feed):
        return
    host.kv_put(FEED_KEY, ([entry] + feed)[:MAX_ENTRIES])


@plugin.on_event
def on_event(call, ev):
    entry = describe(ev, call.tenant.get("include_answers") is True)
    if entry is None:
        return
    entry.update(id=ev.id, at=(ev.occurred_at or datetime.now(timezone.utc)).isoformat())
    append(store(call), entry)


@plugin.webhook("notes")
def notes(call, req):
    secret = str(call.tenant.get("webhook_secret") or "")
    given = req.headers.get("X-Activity-Secret", "")
    if not secret or not hmac.compare_digest(secret, given):
        return WebhookResponse(status=401, body="bad or missing X-Activity-Secret")
    try:
        text = str((req.json() or {}).get("text") or "").strip()
    except ValueError:
        text = ""
    if not text:
        return WebhookResponse(status=400, body='send {"text": "..."}')
    now = datetime.now(timezone.utc)
    append(store(call), {"id": f"note-{now.timestamp()}", "kind": "note", "text": text[:500], "at": now.isoformat()})
    return {"ok": True}


@plugin.ui
def ui(call, req):
    if req.method == "GET" and req.path == "/feed":
        return UIResponse(body=store(call).kv_get(FEED_KEY, []))
    return UIResponse(status=404, body={"error": "not found"})


if __name__ == "__main__":
    plugin.serve()
