import unittest
from unittest.mock import Mock

from weknora_mcp_server import WeKnoraClient


KB_ID = "5a529583-53d9-4cd7-84a5-be21be4ccbb4"
KNOWLEDGE_ID = "193e7baf-76b6-4046-85af-8a338b272032"


def make_client(parse_status="completed", wiki_enabled=True):
    client = WeKnoraClient("http://localhost:8080/api/v1", "test-key")
    client.get_knowledge_base = Mock(
        return_value={"data": {"id": KB_ID, "capabilities": {"wiki": wiki_enabled}}}
    )
    client.get_knowledge = Mock(
        return_value={
            "data": {
                "id": KNOWLEDGE_ID,
                "knowledge_base_id": KB_ID,
                "parse_status": parse_status,
                "title": "source.pdf",
                "error_message": "parse failed" if parse_status == "failed" else "",
            }
        }
    )
    client.wiki_index_view = Mock(return_value={"version": 7})
    client.wiki_log = Mock(return_value={"entries": []})
    client.wiki_summary_exists = Mock(return_value=False)
    return client


class WikiBuildStatusTest(unittest.TestCase):
    def test_completed_when_ingest_event_exists(self):
        client = make_client()
        client.wiki_log.return_value = {
            "entries": [
                {
                    "id": 9,
                    "action": "ingest",
                    "knowledge_id": KNOWLEDGE_ID,
                    "pages_affected": [{"slug": f"summary/{KNOWLEDGE_ID}"}],
                }
            ]
        }

        result = client.get_wiki_build_status(KB_ID, KNOWLEDGE_ID)

        self.assertEqual(result["status"], "completed")
        self.assertTrue(result["ready"])
        self.assertEqual(result["wiki_version"], 7)
        self.assertEqual(result["ingest_event"]["id"], 9)

    def test_processing_while_document_parser_runs(self):
        client = make_client(parse_status="processing")

        result = client.get_wiki_build_status(KB_ID, KNOWLEDGE_ID)

        self.assertEqual(result["status"], "processing")
        self.assertFalse(result["ready"])
        client.wiki_log.assert_not_called()

    def test_processing_after_parse_until_wiki_ingest(self):
        client = make_client()

        result = client.get_wiki_build_status(KB_ID, KNOWLEDGE_ID)

        self.assertEqual(result["status"], "processing")
        self.assertFalse(result["ready"])
        self.assertEqual(
            result["reason"], "Document parsing completed; waiting for Wiki ingest"
        )

    def test_legacy_summary_page_marks_completed(self):
        client = make_client()
        client.wiki_summary_exists.return_value = True

        result = client.get_wiki_build_status(KB_ID, KNOWLEDGE_ID)

        self.assertEqual(result["status"], "completed")
        self.assertTrue(result["ready"])
        self.assertIn("legacy", result["reason"].lower())

    def test_terminal_parse_status_is_not_ready(self):
        for parse_status in ("failed", "cancelled"):
            with self.subTest(parse_status=parse_status):
                client = make_client(parse_status=parse_status)

                result = client.get_wiki_build_status(KB_ID, KNOWLEDGE_ID)

                self.assertEqual(result["status"], parse_status)
                self.assertFalse(result["ready"])

    def test_rejects_knowledge_from_another_kb(self):
        client = make_client()
        client.get_knowledge.return_value["data"]["knowledge_base_id"] = "other-kb"

        with self.assertRaisesRegex(ValueError, "does not belong"):
            client.get_wiki_build_status(KB_ID, KNOWLEDGE_ID)


if __name__ == "__main__":
    unittest.main()
