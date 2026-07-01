import unittest
from types import SimpleNamespace
from unittest.mock import patch

from docreader.parser.doc_parser import SandboxExecutor


class SandboxExecutorProxyTest(unittest.TestCase):
    def test_uses_http_proxy_when_https_proxy_is_empty(self):
        cfg = SimpleNamespace(
            external_https_proxy="",
            external_http_proxy="http://proxy.example.com:8080",
        )
        with patch("docreader.parser.doc_parser.CONFIG", cfg):
            executor = SandboxExecutor()

        self.assertEqual(executor.proxy, "http://proxy.example.com:8080")

    def test_prefers_https_proxy_over_http_proxy(self):
        cfg = SimpleNamespace(
            external_https_proxy="http://proxy.example.com:8443",
            external_http_proxy="http://proxy.example.com:8080",
        )
        with patch("docreader.parser.doc_parser.CONFIG", cfg):
            executor = SandboxExecutor()

        self.assertEqual(executor.proxy, "http://proxy.example.com:8443")


if __name__ == "__main__":
    unittest.main()
