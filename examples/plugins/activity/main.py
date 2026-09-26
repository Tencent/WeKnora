"""A WeKnora plugin that keeps an activity feed of the workspace, written
with the Python SDK. It shows the three ways data reaches a plugin without a
user asking: events WeKnora sends (at least once, so they are deduplicated
by ID), a webhook other systems call, and a page that reads the feed."""

from __future__ import annotations

import hmac
import uuid
from datetime import datetime, timedelta, timezone

from weknora_plugin import ErrorCode, Plugin, PluginError, UIResponse, WebhookResponse

plugin = Plugin("weknora-examples.activity", "1.2.0")

# Each entry is a key of its own under "feed/". One value holding the whole
# feed would hit the store's size limit, and two events arriving together
# would each write back the list without the other's entry.
FEED_KEY = "feed"  # 1.1 kept the whole feed in this one value
ENTRY_PREFIX = "feed/"
MAX_ENTRIES = 100
MAX_TEXT = 500
# Keys sort newest first: they start with the microseconds left until this.
_KEY_EPOCH_US = 10**16
_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def store(call):
    host = call.host()
    if host is None:
        # Retryable: the Host API may just be unreachable for a moment.
        raise PluginError(ErrorCode.UNAVAILABLE, "the Host API is not available to this plugin")
    return host


def clip(text: str, limit: int) -> str:
    text = text.strip()
    return text if len(text) <= limit else text[: limit - 1] + "…"


def describe(ev, include_answers: bool) -> dict | None:
    d = ev.data or {}
    title = clip(str(d.get("title") or d.get("fileName") or d.get("knowledgeId", "")), 200)
    if ev.type == "knowledge.ingested":
        return {"kind": "ingested", "text": title}
    if ev.type == "knowledge.failed":
        return {"kind": "failed", "text": f"{title}: {clip(str(d.get('error') or ''), 280)}".strip(": ")}
    if ev.type == "knowledge.deleted":
        return {"kind": "deleted", "text": title}
    if ev.type == "chat.answered":
        text = clip(str(d.get("question") or ""), 200)
        if include_answers:
            first = str(d.get("answer") or "").strip().splitlines()
            text += f" → {clip(first[0], 200)}" if first else ""
        return {"kind": "answered", "text": text}
    return None  # a type added after this plugin was written


def entry_key(entry: dict) -> str:
    """Where an entry is kept: newest first in key order, then its ID, so a
    repeated delivery of an event lands on the same key."""
    try:
        at = datetime.fromisoformat(entry["at"])
        if at.tzinfo is None:
            at = at.replace(tzinfo=timezone.utc)
        us = (at - _EPOCH) // timedelta(microseconds=1)
    except (KeyError, TypeError, ValueError):
        us = 0
    return f"{ENTRY_PREFIX}{max(_KEY_EPOCH_US - us, 0):016d}/{str(entry['id'])[:120]}"


def append(host, entry: dict) -> None:
    host.kv_put(entry_key(entry), entry)
    recent(host)  # drops what fell off the end


def recent(host) -> list:
    """The newest entries, newest first. Older ones are deleted on the way."""
    page = host.kv_list(FEED_KEY, limit=MAX_ENTRIES)
    if page.entries and page.entries[0].key == FEED_KEY:
        # Move a 1.1 feed to a key per entry, once.
        for old in page.entries[0].value or []:
            if isinstance(old, dict) and old.get("id"):
                host.kv_put(entry_key(old), old)
        host.kv_delete(FEED_KEY)
        page = host.kv_list(FEED_KEY, limit=MAX_ENTRIES)
    after = page.next
    while after:
        older = host.kv_list(ENTRY_PREFIX, after=after)
        for e in older.entries:
            host.kv_delete(e.key)
        after = older.next
    return [e.value for e in page.entries]


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
    now = datetime.now(timezone.utc).isoformat()
    append(store(call), {"id": f"note-{uuid.uuid4().hex}", "kind": "note", "text": clip(text, MAX_TEXT), "at": now})
    return {"ok": True}


@plugin.ui
def ui(call, req):
    if req.method == "GET" and req.path == "/feed":
        return UIResponse(body=recent(store(call)))
    return UIResponse(status=404, body={"error": "not found"})


if __name__ == "__main__":
    plugin.serve()
