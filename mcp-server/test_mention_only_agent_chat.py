import unittest
from unittest.mock import patch

import weknora_mcp_server as srv


class MentionOnlyAgentChatTest(unittest.IsolatedAsyncioTestCase):
    async def test_plain_turn_reaches_agent_without_listing_knowledge_bases(self):
        agent_id = "11111111-1111-1111-1111-111111111111"
        for mode in ("none", "selected"):
            config = {"kb_selection_mode": mode, "retrieve_kb_only_when_mentioned": True}
            with self.subTest(mode=mode), patch.object(
                srv.client, "get_agent", return_value={"data": {"config": config}}
            ), patch.object(srv.client, "list_knowledge_bases", return_value=[]) as owned, patch.object(
                srv.client, "list_shared_knowledge_bases", return_value=[]
            ) as shared, patch.object(srv.client, "agent_chat", return_value={"answer": "hi"}) as chat:
                result = await srv.agent_chat("session", "hello", agent_id)
                self.assertEqual(result["answer"], "hi")
                self.assertIsNone(chat.call_args.kwargs["knowledge_base_ids"])
                owned.assert_not_called()
                shared.assert_not_called()
