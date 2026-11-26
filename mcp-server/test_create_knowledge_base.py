import unittest
from unittest import mock

import weknora_mcp_server as srv


class CreateKnowledgeBaseTest(unittest.TestCase):
    def _posted_body(self, **kwargs):
        with mock.patch.object(srv.client, "_request", return_value={}) as req:
            srv.create_knowledge_base("Docs", "desc", **kwargs)
        req.assert_called_once()
        self.assertEqual(req.call_args.args, ("POST", "/knowledge-bases"))
        return req.call_args.kwargs["json"]

    def test_chunking_matches_web_ui_defaults(self):
        chunking = self._posted_body()["chunking_config"]
        # Same separators and strategy as a KB created in the web UI
        # (frontend/src/views/knowledge/KnowledgeBaseEditorModal.vue). A lone
        # "." never splits CJK text, so such documents ended up in chunks far
        # over chunk_size.
        self.assertEqual(
            chunking["separators"],
            ["\n\n", "\n", "。", "！", "？", ";", "；"],
        )
        self.assertEqual(chunking["strategy"], "auto")
        self.assertEqual(chunking["chunk_size"], 1000)
        self.assertEqual(chunking["chunk_overlap"], 200)

    def test_no_unknown_chunking_keys(self):
        # enable_multimodal is not a ChunkingConfig field; the server drops it.
        chunking = self._posted_body()["chunking_config"]
        self.assertNotIn("enable_multimodal", chunking)

    def test_model_ids_passed_through(self):
        body = self._posted_body(embedding_model_id="emb", summary_model_id="sum")
        self.assertEqual(body["name"], "Docs")
        self.assertEqual(body["description"], "desc")
        self.assertEqual(body["embedding_model_id"], "emb")
        self.assertEqual(body["summary_model_id"], "sum")


if __name__ == "__main__":
    unittest.main()
