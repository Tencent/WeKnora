import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "..", "pluginsdk", "python", "src"))
sys.path.insert(0, HERE)

import main  # noqa: E402
from weknora_plugin import KV_MAX_VALUE_BYTES, UIRequest, kv_value_size  # noqa: E402


class FakeHost:
    def __init__(self):
        self.data = {}

    def kv_get(self, key, default=None):
        return self.data.get(key, default)

    def kv_put(self, key, value, ttl=0):
        assert kv_value_size(value) <= KV_MAX_VALUE_BYTES, f"{key} is {kv_value_size(value)} bytes"
        self.data[key] = value


class LinksTest(unittest.TestCase):
    def setUp(self):
        self.host = FakeHost()
        main.store = lambda call: self.host

    def req(self, mount, method, path, body=None, role="viewer"):
        return main.handle(None, UIRequest(mount=mount, method=method, path=path, body=body, role=role))

    def test_workspace_links(self):
        self.assertEqual(self.req("pages/links", "GET", "/links").body, [])
        links = [{"title": "", "url": "https://github.com/Tencent/WeKnora"}]
        # Only the settings section (admins) writes the workspace's links.
        self.assertEqual(self.req("pages/links", "PUT", "/links", links, role="owner").status, 404)
        saved = self.req("settingsSections/manage", "PUT", "/links", links, role="admin")
        self.assertEqual(saved.body, [{"title": "github.com", "url": "https://github.com/Tencent/WeKnora"}])
        self.assertEqual(self.req("pages/links", "GET", "/links").body, saved.body)

    def test_kb_links(self):
        path = "/kb/kb-1/links"
        self.assertEqual(self.req("kbTabs/links", "PUT", path, [], role="viewer").status, 403)
        self.assertEqual(self.req("kbTabs/links", "PUT", path, [{"title": "Spec", "url": "http://x.example"}], role="contributor").status, 200)
        self.assertEqual(self.req("kbTabs/links", "GET", path).body[0]["title"], "Spec")
        self.assertEqual(self.req("kbTabs/links", "GET", "/kb/kb-2/links").body, [])
        self.assertEqual(self.req("pages/links", "GET", path).status, 404)

    def test_bad_links(self):
        for body in ({"url": "x"}, [{"url": "javascript:alert(1)"}], [{"url": "ftp://x"}], [{}] * 201):
            resp = self.req("settingsSections/manage", "PUT", "/links", body, role="admin")
            self.assertEqual(resp.status, 400, body)

    def test_links_that_fit_in_one_value(self):
        # 200 links with 60-character Chinese titles fit (they did not while
        # the SDK escaped them); 200 links with long URLs do not.
        titled = [{"title": "团队文档" * 15, "url": f"https://docs.example/{i}"} for i in range(main.MAX_LINKS)]
        saved = self.req("settingsSections/manage", "PUT", "/links", titled, role="admin")
        self.assertEqual((saved.status, len(saved.body)), (200, main.MAX_LINKS))
        long = [{"title": "x", "url": "https://docs.example/" + "a" * 1900} for _ in range(main.MAX_LINKS)]
        resp = self.req("settingsSections/manage", "PUT", "/links", long, role="admin")
        self.assertEqual(resp.status, 400)
        self.assertIn("remove some", resp.body["error"])
        self.assertEqual(len(self.req("pages/links", "GET", "/links").body), main.MAX_LINKS)


if __name__ == "__main__":
    unittest.main()
