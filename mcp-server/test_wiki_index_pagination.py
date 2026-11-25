import unittest
from unittest.mock import patch

import weknora_mcp_server as srv


class WikiIndexPaginationTest(unittest.TestCase):
    def test_continue_the_group_returned_by_the_server(self):
        first = {"groups": [{"type": "concept", "next_cursor": "50"}]}
        with patch.object(srv.client, "_request", return_value=first) as request:
            group = srv.wiki_index_view("kb")["groups"][0]
            self.assertEqual(request.call_args.kwargs["params"], {"limit": 50})
            srv.wiki_index_view("kb", page_type=group["type"], cursor=group["next_cursor"])
            self.assertEqual(request.call_args.kwargs["params"],
                             {"limit": 50, "types": "concept", "cursor": "50"})
