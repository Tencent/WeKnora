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
    normalize_wechat_article_html,
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

    def test_extract_markdown_wechat_section_paragraphs(self):
        # WeChat's editor emits <section><span leaf> blocks instead of <p>.
        # Without normalization trafilatura finds no paragraphs inside
        # #js_content and returns the popup/dialog text instead (#4513).
        sections = "".join(
            f'<section style="margin: 0px 8px;"><span leaf="">{line}</span></section>'
            for line in (
                "英文：Generic Object Oriented Substation Event。",
                "中文：面向通用对象的变电站事件，属于 IEC61850 标准里的实时报文。",
                "读音：英文单词 goose。",
                (
                    "它是IEC61850中的一种快速报文传输机制，用于传输变电站内IED之间重要的实时性信号。"
                    "GOOSE采用网络信号代替了常规变电站装置之间硬接线的通信方式，大大简化了变电站二次电缆接线。"
                ),
                "过程层控制通道，传输开关位置、保护跳闸、联锁告警，负责设备分合闸与故障隔离。",
                "≤4ms，满足继电保护的速断性要求。",
                "ST采集断路器、隔刀、SF6告警、主变瓦斯、BMS热失控干接点，转换成GOOSE上传间隔层IED。",
                "保护故障动作下发GOOSE至ST，驱动断路器跳闸隔离故障；后台遥控分合闸指令下发ST执行。",
            )
        )
        html = f"""
        <html><head><title>GOOSE 是什么</title></head><body id="activity-detail">
        <div id="js_article" class="rich_media"><div class="rich_media_inner">
        <div id="page-content" class="rich_media_area_primary">
        <div class="rich_media_area_primary_inner"><div id="img-content" class="rich_media_wrp">
        <h1 class="rich_media_title">GOOSE 是什么</h1>
        <div class="rich_media_content js_underline_content" id="js_content" style="">
        <section><h1><span leaf="">一、定义</span></h1></section>{sections}
        </div></div></div></div></div></div>
        <div class="weui-dialog"><div class="weui-dialog__bd">
        <p>微信扫一扫 关注该公众号</p>
        <p>当前内容可能存在未经审核的第三方商业营销信息，请确认是否继续访问。</p>
        <p>你可以补充投诉原因</p></div></div>
        </body></html>
        """
        md = extract_markdown_from_html(html)
        self.assertIsNotNone(md)
        self.assertIn("读音：英文单词 goose。", md)
        self.assertIn("≤4ms", md)
        self.assertNotIn("微信扫一扫", md)

    def test_normalize_wechat_html_leaves_other_pages_untouched(self):
        html = "<html><body><section><span>hello</span></section></body></html>"
        self.assertEqual(normalize_wechat_article_html(html), html)

    def test_normalize_wechat_html_keeps_container_sections(self):
        html = (
            '<html><body><div id="js_content">'
            "<section><section><span>a</span></section><p>b</p></section>"
            "</div></body></html>"
        )
        out = normalize_wechat_article_html(html)
        self.assertIn("<section><p><span>a</span></p><p>b</p></section>", out)

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
