"""HTTP errors must not become URL knowledge; browser/network are fakes."""
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from docreader.parser.web_parser import WebParseError, WebParser

ERROR_TEXT = "This page could not be found. " * 8
OK_TEXT = "This is a useful documentation page with a detailed explanation. " * 4
TEST_URL = "https://example.com/article?secret=SYNTHETIC"


class AsyncContext:
    def __init__(self, value):
        self.value = value

    async def __aenter__(self):
        return self.value

    async def __aexit__(self, *exc):
        pass


class NavigationStatusTest(unittest.TestCase):
    def run_parser(self, status, *, none=False, empty=False, navigation_error=None):
        title = "Documentation"
        text = ERROR_TEXT if status and status >= 400 else OK_TEXT
        html = (
            f"<html><head><title>{title}</title></head><body>"
            f"<article><h1>{title}</h1><p>{text}</p></article></body></html>"
        )
        # A goto response represents Playwright's first non-redirect response.
        page = SimpleNamespace(
            goto=AsyncMock(
                return_value=None if none else SimpleNamespace(status=status),
                side_effect=navigation_error,
            ),
            title=AsyncMock(return_value=title),
            content=AsyncMock(return_value="" if empty else html),
        )
        browser = SimpleNamespace(new_page=AsyncMock(return_value=page), close=AsyncMock())
        driver = SimpleNamespace(webkit=SimpleNamespace(launch=AsyncMock(return_value=browser)))
        proxy = AsyncContext(SimpleNamespace(url="http://proxy"))
        with (
            patch("docreader.parser.web_parser.is_ssrf_safe_url", return_value=(True, "")),
            patch("docreader.parser.web_parser.SSRFProxy", return_value=proxy),
            patch("docreader.parser.web_parser.async_playwright", return_value=AsyncContext(driver)),
            patch("docreader.parser.web_parser.install_ssrf_route_guard", new=AsyncMock()),
            patch("docreader.parser.web_parser.wait_for_rendered_content", new=AsyncMock()),
            patch("docreader.parser.web_parser.read_visible_text", new=AsyncMock(return_value="" if empty else text)),
        ):
            try:
                return WebParser(title="test").parse_into_text(TEST_URL.encode())
            finally:
                self.page, self.browser = page, browser

    def test_http_error_page_fails_without_reading_body(self):
        for status in (400, 401, 403, 404, 429, 499, 500, 503, 599):
            with self.subTest(status=status):
                with self.assertRaisesRegex(WebParseError, str(status)) as error:
                    self.run_parser(status)
                self.assertNotIn("example.com", str(error.exception))
                self.assertNotIn("SYNTHETIC", str(error.exception))
                self.assertNotIn(ERROR_TEXT, str(error.exception))
                self.page.content.assert_not_awaited()
                self.page.title.assert_not_awaited()
                self.browser.close.assert_awaited_once()

    def test_navigation_exception_still_fails(self):
        with self.assertRaisesRegex(WebParseError, "navigation failed"):
            self.run_parser(200, navigation_error=RuntimeError("synthetic timeout"))
        self.browser.close.assert_awaited_once()

    def test_successful_final_response_is_parsed(self):
        doc = self.run_parser(200)
        self.assertIn("useful documentation page", doc.content)
        self.browser.close.assert_awaited_once()

    def test_non_error_status_keeps_existing_behavior(self):
        for status in (201, 204, 206, 299, 300, 304, 399):
            with self.subTest(status=status):
                doc = self.run_parser(status)
                self.assertIn("useful documentation page", doc.content)
                self.browser.close.assert_awaited_once()

    def test_empty_non_error_response_fails_as_before(self):
        for status in (204, 304, None):
            with self.subTest(status=status):
                with self.assertRaisesRegex(WebParseError, "no HTML or visible text"):
                    self.run_parser(status, none=status is None, empty=True)
                self.browser.close.assert_awaited_once()

    def test_file_and_data_urls_stay_blocked(self):
        from docreader.utils.ssrf import is_ssrf_safe_url

        for url in ("file:///etc/passwd", "data:text/html,hello"):
            with self.subTest(url=url):
                self.assertFalse(is_ssrf_safe_url(url)[0])

    def test_no_response_keeps_existing_behavior(self):
        doc = self.run_parser(None, none=True)
        self.assertIn("useful documentation page", doc.content)
        self.browser.close.assert_awaited_once()


class RPCFailurePropagationTest(unittest.TestCase):
    run_parser = NavigationStatusTest.run_parser

    def test_http_failure_is_sent_as_unary_and_stream_error(self):
        from docreader.main import DocReaderServicer
        from docreader.proto.docreader_pb2 import ReadRequest

        servicer = DocReaderServicer.__new__(DocReaderServicer)
        servicer.parser = SimpleNamespace(parse_url=lambda *args, **kwargs: self.run_parser(404))
        request = ReadRequest(url=TEST_URL, title="test")
        unary = servicer.Read(request, None)
        self.assertIn("HTTP 404", unary.error)
        self.assertNotIn("SYNTHETIC", unary.error)
        self.assertEqual("", unary.markdown_content)
        stream = list(servicer.ReadStream(request, None))
        self.assertEqual(1, len(stream))
        self.assertIn("HTTP 404", stream[0].meta.error)
        self.assertNotIn("SYNTHETIC", stream[0].meta.error)
        self.assertEqual("", stream[0].meta.markdown_content)
