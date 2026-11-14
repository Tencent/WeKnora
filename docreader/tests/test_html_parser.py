import unittest
from dataclasses import replace
from pathlib import Path
from unittest.mock import patch

import docreader.parser.html_parser as html_parser
from docreader.config import CONFIG
from docreader.parser.html_parser import (
    _MIN_EXTRACTED_CHARS,
    _MIN_KEEP_RATIO,
    HTMLParser,
    extract_main_content,
)
from docreader.parser.registry import BUILTIN_ENGINE, registry

REPO_ROOT = Path(__file__).resolve().parents[2]
MAIN_CONTENT_FIXTURES = REPO_ROOT / "docreader" / "testdata" / "html_main_content"


def _parse_bytes(
    content: bytes, *, main_content: bool, file_name: str = "page.html"
) -> str:
    """Parse HTML through the upload path with the switch forced."""
    with patch.object(
        html_parser, "CONFIG", replace(CONFIG, html_main_content=main_content)
    ):
        return HTMLParser(file_name=file_name, file_type="html").parse(content).content


def _parse_uploaded_html(name: str, *, main_content: bool) -> str:
    """Parse a fixture through the upload path with the switch forced."""
    return _parse_bytes(
        (MAIN_CONTENT_FIXTURES / name).read_bytes(),
        main_content=main_content,
        file_name=name,
    )


class HTMLParserTest(unittest.TestCase):
    def test_parse_static_html_fixture_into_markdown(self):
        content = (REPO_ROOT / "docreader" / "testdata" / "test.html").read_bytes()

        document = HTMLParser(file_name="test.html", file_type="html").parse(content)

        self.assertIn("# 测试 HTML 文档", document.content)
        self.assertIn("[测试链接](https://example.com)", document.content)
        self.assertIn("| 表头1 | 表头2 |", document.content)
        self.assertIn("内容4", document.content)

    def test_parse_empty_html_returns_empty_document(self):
        document = HTMLParser(file_name="empty.htm", file_type="htm").parse(b"")

        self.assertEqual("", document.content)

    def test_parse_honors_declared_windows_1252_charset(self):
        html = (
            '<meta charset="windows-1252">'
            "<h1>R\u00e9sum\u00e9 \u201cquoted\u201d \u20ac</h1>"
        ).encode("windows-1252")

        document = HTMLParser(file_name="legacy.html", file_type="html").parse(html)

        self.assertIn("# R\u00e9sum\u00e9 \u201cquoted\u201d \u20ac", document.content)

    def test_parse_honors_utf16_bom(self):
        html = "<html><body><h1>UTF-16 title</h1></body></html>".encode("utf-16")

        document = HTMLParser(file_name="utf16.html", file_type="html").parse(html)

        self.assertIn("# UTF-16 title", document.content)
        self.assertNotIn("\x00", document.content)

    def test_parse_script_only_html_returns_empty_document(self):
        html = (
            b"<html><body><script>alert('x')</script><style>p{}</style></body></html>"
        )

        document = HTMLParser(file_name="script.html", file_type="html").parse(html)

        self.assertEqual("", document.content)

    def test_parse_preserves_relative_and_fragment_links(self):
        html = b'<a href="./guide.html">Guide</a><a href="#section">Section</a>'

        document = HTMLParser(file_name="links.html", file_type="html").parse(html)

        self.assertIn("[Guide](./guide.html)", document.content)
        self.assertIn("[Section](#section)", document.content)

    def test_parse_keeps_space_held_by_whitespace_only_inline_element(self):
        html = b"<p>further<strong> </strong>reference</p>"

        document = HTMLParser(file_name="space.html", file_type="html").parse(html)

        self.assertEqual("further reference", document.content)

    def test_registry_routes_html_extensions_to_html_parser(self):
        self.assertIs(registry.get_parser_class("", "html"), HTMLParser)
        self.assertIs(registry.get_parser_class("", "htm"), HTMLParser)

        builtin_types = registry._engines[BUILTIN_ENGINE]
        self.assertIn("html", builtin_types)
        self.assertIn("htm", builtin_types)


class HTMLMainContentTest(unittest.TestCase):
    """DOCREADER_HTML_MAIN_CONTENT: opt-in main-content extraction on upload."""

    def test_upload_indexes_the_whole_page_by_default(self):
        content = _parse_uploaded_html("article_page.html", main_content=False)

        # Header nav, sidebar and footer all reach the index, as they always did.
        self.assertIn("产品定价", content)
        self.assertIn("订阅我们的新闻邮件", content)
        self.assertIn("京ICP备00000000号-1", content)
        self.assertIn("混合检索", content)

    def test_enabled_extraction_drops_page_chrome_and_keeps_the_article(self):
        full = _parse_uploaded_html("article_page.html", main_content=False)
        content = _parse_uploaded_html("article_page.html", main_content=True)

        self.assertNotIn("产品定价", content)  # header nav
        self.assertNotIn("订阅我们的新闻邮件", content)  # sidebar
        self.assertNotIn("京ICP备00000000号-1", content)  # footer
        # The article body survives in full, table and links included.
        self.assertIn("# 深入理解 RAG 的召回策略", content)
        self.assertIn("混合检索把 BM25 的稀疏信号与向量召回融合在一起", content)
        self.assertIn("重排模型只对前 N 条候选打分", content)
        self.assertIn("| 策略 | 优点 | 代价 |", content)
        self.assertIn("两路互补，召回率最高", content)
        self.assertIn("[分块器的实现细节](/blog/99)", content)
        self.assertLess(len(content), len(full))

    def test_enabled_extraction_preserves_body_image_references(self):
        html = (MAIN_CONTENT_FIXTURES / "article_page.html").read_text(encoding="utf-8")
        html = html.replace(
            "</article>",
            '<figure><img src="https://example.com/chart.png" alt="Recall comparison" /></figure>'
            '<figure><img src="images/local-chart.png" alt="Local comparison" /></figure>'
            '</article>',
        )
        self.assertIn("https://example.com/chart.png", html)
        content = _parse_bytes(html.encode(), main_content=True)
        self.assertNotIn("产品定价", content)
        self.assertIn("https://example.com/chart.png", content)
        self.assertIn("images/local-chart.png", content)
        self.assertIn("混合检索", content)

    def test_short_article_does_not_index_duplicate_body_text(self):
        body = "正文说明知识库如何解析上传文件，以及如何把检索到的内容交给模型生成答案。" * 9
        html = f"<html><body><article><h1>知识库解析说明</h1><p>{body}</p></article></body></html>".encode()
        content = _parse_bytes(html, main_content=True)
        self.assertEqual(content.count(body), 1)

    def test_extractor_introduced_repetition_falls_back_but_source_repetition_does_not(self):
        body = "A long paragraph about retrieval, citations and document parsing. " * 5
        html = f"<article><p>{body}</p></article>".encode()
        full = _parse_bytes(html, main_content=False)
        repeated = body.strip() + "\n\n" + body.strip()
        with patch.object(html_parser, "extract_main_content", return_value=repeated):
            self.assertEqual(_parse_bytes(html, main_content=True), full)
            original_repeated = f"<article><p>{body}</p><p>{body}</p></article>".encode()
            content = _parse_bytes(original_repeated, main_content=True)
            self.assertEqual(content.count(body.strip()), 2)

    def test_falls_back_to_full_conversion_without_an_article_body(self):
        for name, expected in (
            ("form_page.html", "忘记密码？"),
            ("link_list_page.html", "第六章 常见问题"),
        ):
            with self.subTest(fixture=name):
                full = _parse_uploaded_html(name, main_content=False)
                content = _parse_uploaded_html(name, main_content=True)

                self.assertEqual(content, full)
                self.assertIn(expected, content)

    def test_falls_back_when_extraction_keeps_too_little_of_a_long_page(self):
        full = _parse_uploaded_html("chrome_heavy_page.html", main_content=False)
        content = _parse_uploaded_html("chrome_heavy_page.html", main_content=True)

        # The length floor is passed here: what keeps the full conversion is the
        # ratio guard against "trafilatura kept only a fragment of a long page".
        html = (MAIN_CONTENT_FIXTURES / "chrome_heavy_page.html").read_text(
            encoding="utf-8"
        )
        extracted = extract_main_content(html)
        self.assertGreaterEqual(len(extracted), _MIN_EXTRACTED_CHARS)
        self.assertLess(len(extracted), len(full) * _MIN_KEEP_RATIO)

        self.assertEqual(content, full)
        self.assertIn("批量导入任务在遇到空文件时不再中断整个批次", content)
        self.assertIn("京ICP备00000000号-1", content)

    def test_short_page_is_not_reduced_to_nothing(self):
        full = _parse_uploaded_html("short_notice.html", main_content=False)
        content = _parse_uploaded_html("short_notice.html", main_content=True)

        self.assertEqual(content, full)
        self.assertIn(
            "本服务将于 10 月 1 日 02:00 至 04:00 维护，期间暂停文档上传与检索。",
            content,
        )

    def test_empty_html_stays_empty(self):
        content = _parse_bytes(b"", main_content=True, file_name="empty.html")

        self.assertEqual("", content)


if __name__ == "__main__":
    unittest.main()
