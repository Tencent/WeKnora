import unittest

from docreader.parser.web_parser import (
    build_visible_text_fallback,
    extract_markdown_from_html,
)


class TestWebParserHelpers(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
