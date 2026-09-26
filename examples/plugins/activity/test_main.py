import json
import os
import sys
import threading
import unittest
from datetime import datetime, timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "..", "pluginsdk", "python", "src"))
sys.path.insert(0, HERE)

import main  # noqa: E402
from weknora_plugin import (  # noqa: E402
    KV_MAX_KEY_BYTES,
    KV_MAX_VALUE_BYTES,
    EventDelivery,
    KVEntry,
    KVList,
    UIRequest,
    WebhookRequest,
    kv_value_size,
)
from weknora_plugin.plugin import Call  # noqa: E402


class FakeHost:
    """The key-value store as the Host API serves it, with its limits."""

    def __init__(self):
        self.data = {}
        self.lock = threading.Lock()

    def kv_get(self, key, default=None):
        with self.lock:
            return self.data.get(key, default)

    def kv_put(self, key, value, ttl=0):
        assert len(key.encode()) <= KV_MAX_KEY_BYTES, key
        assert kv_value_size(value) <= KV_MAX_VALUE_BYTES, f"{key} is {kv_value_size(value)} bytes"
        with self.lock:
            self.data[key] = json.loads(json.dumps(value))

    def kv_delete(self, key):
        with self.lock:
            self.data.pop(key, None)

    def kv_list(self, prefix="", after="", limit=0):
        limit = limit or 100
        with self.lock:
            keys = sorted(k for k in self.data if k.startswith(prefix) and k > after)
            page = [KVEntry(key=k, value=self.data[k]) for k in keys[:limit]]
        return KVList(entries=page, next=keys[limit - 1] if len(keys) > limit else "")


def call(**tenant):
    return Call({"context": {"tenantId": 1}, "config": {"tenant": tenant}})


class ActivityTest(unittest.TestCase):
    def setUp(self):
        self.host = FakeHost()
        main.store = lambda c: self.host

    def feed(self):
        return main.recent(self.host)

    def test_events_are_recorded_once(self):
        # A retry after a failure repeats the delivery, time and all.
        at = datetime(2026, 9, 1, tzinfo=timezone.utc)
        ev = EventDelivery(id="e1", type="knowledge.ingested", occurred_at=at, data={"knowledgeId": "k1", "title": "Spec"})
        main.on_event(call(), ev)
        main.on_event(call(), ev)
        main.on_event(call(), EventDelivery(id="e2", type="some.future.type", data={}))
        self.assertEqual([(e["id"], e["kind"], e["text"]) for e in self.feed()], [("e1", "ingested", "Spec")])

    def test_answers(self):
        at = datetime(2026, 9, 1, tzinfo=timezone.utc)
        data = {"question": "What is RAG?", "answer": "Retrieval.\nMore"}
        main.on_event(call(), EventDelivery(id="a", type="chat.answered", occurred_at=at, data=data))
        later = at + timedelta(microseconds=1)
        main.on_event(call(include_answers=True), EventDelivery(id="b", type="chat.answered", occurred_at=later, data=data))
        self.assertEqual([e["text"] for e in self.feed()], ["What is RAG? → Retrieval.", "What is RAG?"])

    def test_note_webhook(self):
        req = WebhookRequest(method="POST", headers={"X-Activity-Secret": "s3"}, body=json.dumps({"text": "Deploy done"}).encode())
        self.assertEqual(main.notes(call(webhook_secret="s3"), req), {"ok": True})
        self.assertEqual(self.feed()[0]["text"], "Deploy done")
        self.assertEqual(main.notes(call(webhook_secret="other"), req).status, 401)
        self.assertEqual(main.notes(call(), req).status, 401)
        self.assertEqual(main.notes(call(webhook_secret="s3"), WebhookRequest(headers={"X-Activity-Secret": "s3"}, body=b"{}")).status, 400)

    def test_page_reads_the_feed(self):
        main.on_event(call(), EventDelivery(id="e1", type="knowledge.deleted", data={"title": "Old"}))
        self.assertEqual(main.ui(call(), UIRequest(method="GET", path="/feed")).body[0]["kind"], "deleted")
        self.assertEqual(main.ui(call(), UIRequest(method="GET", path="/other")).status, 404)

    def test_long_feeds_of_chinese_text_keep_the_newest(self):
        start = datetime(2026, 9, 1, tzinfo=timezone.utc)
        for i in range(130):
            title = f"{i} " + "文档标题" * 100
            ev = EventDelivery(
                id=f"e{i}", type="knowledge.failed", occurred_at=start + timedelta(minutes=i),
                data={"title": title, "error": "解析失败" * 500},
            )
            main.on_event(call(), ev)
        feed = self.feed()
        self.assertEqual(len(feed), main.MAX_ENTRIES)
        self.assertEqual([e["id"] for e in feed[:2]], ["e129", "e128"])
        self.assertEqual(feed[-1]["id"], "e30")
        self.assertLessEqual(len(feed[0]["text"]), main.MAX_TEXT)
        self.assertEqual(len(self.host.data), main.MAX_ENTRIES)

    def test_concurrent_deliveries_keep_every_entry(self):
        def deliver(i):
            main.on_event(call(), EventDelivery(id=f"e{i}", type="knowledge.ingested", data={"title": str(i)}))

        threads = [threading.Thread(target=deliver, args=(i,)) for i in range(40)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(sorted(e["id"] for e in self.feed()), sorted(f"e{i}" for i in range(40)))

    def test_a_1_1_feed_moves_to_a_key_per_entry(self):
        self.host.data["feed"] = [
            {"id": "b", "kind": "note", "text": "newer", "at": "2026-09-02T00:00:00+00:00"},
            {"id": "a", "kind": "note", "text": "older", "at": "2026-09-01T00:00:00+00:00"},
        ]
        main.on_event(call(), EventDelivery(
            id="c", type="knowledge.deleted", occurred_at=datetime(2026, 9, 3, tzinfo=timezone.utc), data={"title": "x"},
        ))
        self.assertEqual([e["id"] for e in self.feed()], ["c", "b", "a"])
        self.assertNotIn("feed", self.host.data)


if __name__ == "__main__":
    unittest.main()
