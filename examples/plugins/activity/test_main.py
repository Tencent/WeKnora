import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "..", "pluginsdk", "python", "src"))
sys.path.insert(0, HERE)

import main  # noqa: E402
from weknora_plugin import EventDelivery, UIRequest, WebhookRequest  # noqa: E402
from weknora_plugin.plugin import Call  # noqa: E402


class FakeHost:
    def __init__(self):
        self.data = {}

    def kv_get(self, key, default=None):
        return self.data.get(key, default)

    def kv_put(self, key, value, ttl=0):
        self.data[key] = value


def call(**tenant):
    return Call({"context": {"tenantId": 1}, "config": {"tenant": tenant}})


class ActivityTest(unittest.TestCase):
    def setUp(self):
        self.host = FakeHost()
        main.store = lambda c: self.host

    def feed(self):
        return self.host.data.get("feed", [])

    def test_events_are_recorded_once(self):
        ev = EventDelivery(id="e1", type="knowledge.ingested", data={"knowledgeId": "k1", "title": "Spec"})
        main.on_event(call(), ev)
        main.on_event(call(), ev)  # a retry after a failure
        main.on_event(call(), EventDelivery(id="e2", type="some.future.type", data={}))
        self.assertEqual([(e["id"], e["kind"], e["text"]) for e in self.feed()], [("e1", "ingested", "Spec")])

    def test_answers(self):
        ev = EventDelivery(id="a", type="chat.answered", data={"question": "What is RAG?", "answer": "Retrieval.\nMore"})
        main.on_event(call(), ev)
        main.on_event(call(include_answers=True), EventDelivery(id="b", type="chat.answered", data=ev.data))
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


if __name__ == "__main__":
    unittest.main()
