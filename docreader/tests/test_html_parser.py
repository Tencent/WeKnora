import unittest
from pathlib import Path

from docreader.parser.html_parser import HTMLParser
from docreader.parser.registry import BUILTIN_ENGINE, registry

REPO_ROOT = Path(__file__).resolve().parents[2]


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

    def test_registry_routes_html_extensions_to_html_parser(self):
        self.assertIs(registry.get_parser_class("", "html"), HTMLParser)
        self.assertIs(registry.get_parser_class("", "htm"), HTMLParser)

        builtin_types = registry._engines[BUILTIN_ENGINE]
        self.assertIn("html", builtin_types)
        self.assertIn("htm", builtin_types)


if __name__ == "__main__":
    unittest.main()
