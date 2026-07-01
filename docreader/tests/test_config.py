import os
import unittest
from unittest.mock import patch

from docreader import config


class DocReaderConfigTest(unittest.TestCase):
    def test_parser_concurrency_defaults_are_conservative(self):
        with patch.dict(os.environ, {}, clear=True):
            cfg = config.load_config()

        self.assertEqual(cfg.markitdown_max_workers, 1)
        self.assertEqual(cfg.pdf_render_max_workers, 1)
        self.assertEqual(cfg.pdf_render_dpi, 200)
        self.assertEqual(cfg.pdf_jpeg_quality, 85)

    def test_loads_parser_concurrency_env(self):
        env = {
            "DOCREADER_MARKITDOWN_MAX_WORKERS": "3",
            "DOCREADER_PDF_RENDER_MAX_WORKERS": "2",
            "DOCREADER_PDF_RENDER_DPI": "180",
            "DOCREADER_PDF_JPEG_QUALITY": "85",
        }
        with patch.dict(os.environ, env):
            cfg = config.load_config()

        self.assertEqual(cfg.markitdown_max_workers, 3)
        self.assertEqual(cfg.pdf_render_max_workers, 2)
        self.assertEqual(cfg.pdf_render_dpi, 180)
        self.assertEqual(cfg.pdf_jpeg_quality, 85)

    def test_dump_config_includes_parser_limits(self):
        dumped = config.dump_config()

        self.assertIn("DOCREADER_MARKITDOWN_MAX_WORKERS", dumped)
        self.assertIn("DOCREADER_PDF_RENDER_MAX_WORKERS", dumped)
        self.assertIn("DOCREADER_PDF_RENDER_DPI", dumped)
        self.assertIn("DOCREADER_PDF_JPEG_QUALITY", dumped)

    def test_proxy_aliases_ignore_empty_specific_values(self):
        env = {
            "DOCREADER_EXTERNAL_HTTP_PROXY": "",
            "EXTERNAL_HTTP_PROXY": "http://proxy.example.com:8080",
            "DOCREADER_EXTERNAL_HTTPS_PROXY": "",
            "EXTERNAL_HTTPS_PROXY": "http://proxy.example.com:8443",
        }
        with patch.dict(os.environ, env, clear=True):
            cfg = config.load_config()

        self.assertEqual(cfg.external_http_proxy, "http://proxy.example.com:8080")
        self.assertEqual(cfg.external_https_proxy, "http://proxy.example.com:8443")

    def test_docreader_proxy_vars_take_priority_over_aliases(self):
        env = {
            "DOCREADER_EXTERNAL_HTTP_PROXY": "http://doc.proxy.example.com:8080",
            "EXTERNAL_HTTP_PROXY": "http://proxy.example.com:8080",
            "DOCREADER_EXTERNAL_HTTPS_PROXY": "http://doc.proxy.example.com:8443",
            "EXTERNAL_HTTPS_PROXY": "http://proxy.example.com:8443",
        }
        with patch.dict(os.environ, env, clear=True):
            cfg = config.load_config()

        self.assertEqual(cfg.external_http_proxy, "http://doc.proxy.example.com:8080")
        self.assertEqual(cfg.external_https_proxy, "http://doc.proxy.example.com:8443")


if __name__ == "__main__":
    unittest.main()
