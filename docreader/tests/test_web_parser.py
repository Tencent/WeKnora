import os
import unittest
from unittest.mock import AsyncMock, patch

from docreader.parser.web_parser import (
    StdWebParser,
    WebParseError,
    WebParser,
    _ScrapeResult,
    build_visible_text_fallback,
    extract_markdown_from_html,
    install_ssrf_route_guard,
)
from docreader.utils.ssrf import is_ssrf_safe_url, reset_ssrf_whitelist_cache_for_test


class TestWebParserHelpers(unittest.TestCase):
    def setUp(self) -> None:
        self._env_patch = patch.dict(
            os.environ,
            {"SSRF_WHITELIST": "", "SSRF_WHITELIST_EXTRA": ""},
            clear=False,
        )
        self._env_patch.start()
        reset_ssrf_whitelist_cache_for_test()

    def tearDown(self) -> None:
        self._env_patch.stop()
        reset_ssrf_whitelist_cache_for_test()

    def test_extract_markdown_empty_html(self):
        self.assertIsNone(extract_markdown_from_html(""))
        self.assertIsNone(extract_markdown_from_html("   "))

    def test_extract_markdown_article_html(self):
        html = """
        <html><head><title>Demo</title></head><body>
        <article><h1>Hello</h1><p>World paragraph with enough text for extraction.</p></article>
        </body></html>
        """
        md = extract_markdown_from_html(html)
        self.assertIsNotNone(md)
        self.assertIn("Hello", md)

    def test_build_fallback_too_short(self):
        self.assertIsNone(build_visible_text_fallback("short"))
        self.assertIsNone(build_visible_text_fallback(""))

    def test_build_fallback_with_title(self):
        text = "A" * 60
        md = build_visible_text_fallback(text, page_title="WeKnora")
        self.assertIsNotNone(md)
        self.assertTrue(md.startswith("# WeKnora"))
        self.assertIn(text, md)

    def test_build_fallback_without_title(self):
        text = "B" * 60
        md = build_visible_text_fallback(text, page_title="")
        self.assertEqual(md, text)

    def test_install_ssrf_route_guard_is_importable(self):
        self.assertTrue(callable(install_ssrf_route_guard))

    def test_redirect_target_blocked_before_navigation(self):
        safe, reason = is_ssrf_safe_url("http://127.0.0.1:39127/audit.txt")
        self.assertFalse(safe)
        self.assertTrue(reason)


class TestStdWebParserFailures(unittest.TestCase):
    """Scrape/parse failures must raise, not become indexable document body."""

    def _parser(self) -> StdWebParser:
        return StdWebParser(title="page")

    def test_empty_scrape_raises_instead_of_error_body(self):
        empty = _ScrapeResult(
            html="",
            visible_text="",
            page_title="",
            error="navigation failed: Timeout",
        )
        parser = self._parser()
        with patch.object(parser, "scrape", new=AsyncMock(return_value=empty)):
            with self.assertRaises(WebParseError) as ctx:
                parser.parse_into_text(b"https://example.com/blocked")
        message = str(ctx.exception)
        self.assertIn("https://example.com/blocked", message)
        self.assertIn("navigation failed", message)
        self.assertNotIn("Error parsing web page:", message)

    def test_empty_scrape_without_error_field_still_raises(self):
        empty = _ScrapeResult(html="", visible_text="", page_title="")
        parser = self._parser()
        with patch.object(parser, "scrape", new=AsyncMock(return_value=empty)):
            with self.assertRaises(WebParseError) as ctx:
                parser.parse_into_text(b"https://example.com/blank")
        self.assertIn("no HTML or visible text", str(ctx.exception))

    def test_unextractable_page_raises_instead_of_error_body(self):
        scrape = _ScrapeResult(
            html="<html><body><div></div></body></html>",
            visible_text="short",
            page_title="",
        )
        parser = self._parser()
        with patch.object(parser, "scrape", new=AsyncMock(return_value=scrape)):
            with patch(
                "docreader.parser.web_parser.extract_markdown_from_html",
                return_value=None,
            ):
                with patch(
                    "docreader.parser.web_parser.build_visible_text_fallback",
                    return_value=None,
                ):
                    with self.assertRaises(WebParseError) as ctx:
                        parser.parse_into_text(b"https://example.com/empty")
        self.assertIn("Failed to parse web page", str(ctx.exception))
        self.assertIn("https://example.com/empty", str(ctx.exception))

    def test_successful_scrape_returns_markdown_document(self):
        html = """
        <html><head><title>Demo</title></head><body>
        <article><h1>Hello</h1><p>World paragraph with enough text for extraction.</p></article>
        </body></html>
        """
        scrape = _ScrapeResult(
            html=html,
            visible_text="Hello World paragraph with enough text for extraction.",
            page_title="Demo",
        )
        parser = self._parser()
        with patch.object(parser, "scrape", new=AsyncMock(return_value=scrape)):
            doc = parser.parse_into_text(b"https://example.com/ok")
        self.assertTrue(doc.is_valid())
        self.assertNotIn("Error parsing web page:", doc.content)
        self.assertIn("Hello", doc.content)

    def test_pipeline_web_parser_does_not_index_scrape_error(self):
        empty = _ScrapeResult(
            html="",
            visible_text="",
            page_title="",
            error="URL blocked by SSRF guard: loopback",
        )
        pipeline = WebParser(title="page")
        with patch.object(StdWebParser, "scrape", new=AsyncMock(return_value=empty)):
            with self.assertRaises(WebParseError):
                pipeline.parse_into_text(b"https://example.com/ssrf")


if __name__ == "__main__":
    unittest.main()
