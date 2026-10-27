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

    def test_html_main_content_is_off_by_default(self):
        with patch.dict(os.environ, {}, clear=True):
            cfg = config.load_config()

        self.assertFalse(cfg.html_main_content)

    def test_loads_html_main_content_env(self):
        with patch.dict(os.environ, {"DOCREADER_HTML_MAIN_CONTENT": "true"}):
            cfg = config.load_config()

        self.assertTrue(cfg.html_main_content)

    def test_dump_config_includes_html_main_content(self):
        dumped = config.dump_config()

        self.assertIn("DOCREADER_HTML_MAIN_CONTENT", dumped)

    def test_preprocess_limits_are_independent_and_bounded(self):
        with patch.dict(os.environ, {"DOCREADER_GRPC_MAX_WORKERS": "8"}, clear=True):
            cfg = config.load_config()
        self.assertEqual(cfg.preprocess_max_workers, 1)
        self.assertEqual(cfg.preprocess_threads, 2)
        self.assertEqual(cfg.preprocess_timeout_seconds, 30)
        self.assertEqual(cfg.preprocess_memory_mb, 2048)
        self.assertEqual(cfg.preprocess_max_pixels, 16_000_000)
        self.assertEqual(cfg.preprocess_max_edge, 2000)
        self.assertEqual(cfg.preprocess_probe_edge, 2000)

    def test_preprocess_limits_cannot_be_disabled_by_invalid_values(self):
        for name in ("MAX_WORKERS", "THREADS", "TIMEOUT_SECONDS", "MEMORY_MB", "MAX_PIXELS", "MAX_EDGE", "PROBE_EDGE"):
            key = f"DOCREADER_PREPROCESS_{name}"
            for value in ("0", "-1", "invalid"):
                with self.subTest(key=key, value=value), patch.dict(os.environ, {key: value}, clear=True):
                    cfg = config.load_config()
                    self.assertGreater(getattr(cfg, f"preprocess_{name.lower()}"), 0)
            with self.subTest(key=key), patch.dict(os.environ, {key: "7"}, clear=True):
                self.assertEqual(getattr(config.load_config(), f"preprocess_{name.lower()}"), 7)
            self.assertIn(key, config.dump_config())


class FileSizeConfigTest(unittest.TestCase):
    def _load_file_size(self, env, expected_mb):
        with patch.dict(os.environ, env, clear=True):
            cfg = config.load_config()
        # The gRPC options still consume the converted byte count.
        self.assertEqual(cfg.grpc_max_file_size_mb, expected_mb * 1024 * 1024)
        return cfg

    def test_dump_reports_default_file_size_in_mb(self):
        cfg = self._load_file_size({}, 50)
        with patch.object(config, "CONFIG", cfg):
            self.assertEqual(config.dump_config()["DOCREADER_GRPC_MAX_FILE_SIZE_MB"], 50)

    def test_dump_reports_namespaced_file_size_in_mb(self):
        cfg = self._load_file_size({"DOCREADER_GRPC_MAX_FILE_SIZE_MB": "7"}, 7)
        with patch.object(config, "CONFIG", cfg):
            self.assertEqual(config.dump_config()["DOCREADER_GRPC_MAX_FILE_SIZE_MB"], 7)

    def test_dump_reports_legacy_file_size_in_mb(self):
        cfg = self._load_file_size({"MAX_FILE_SIZE_MB": "3"}, 3)
        with patch.object(config, "CONFIG", cfg):
            self.assertEqual(config.dump_config()["DOCREADER_GRPC_MAX_FILE_SIZE_MB"], 3)

    def test_startup_log_reports_file_size_in_mb(self):
        cfg = self._load_file_size({"DOCREADER_GRPC_MAX_FILE_SIZE_MB": "7"}, 7)
        with patch.object(config, "CONFIG", cfg), self.assertLogs(config.logger, level="INFO") as logs:
            config.print_config()
        self.assertIn("INFO:docreader.config:DOCREADER_GRPC_MAX_FILE_SIZE_MB=7", logs.output)


if __name__ == "__main__":
    unittest.main()
