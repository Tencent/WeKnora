"""The ZIP fallback must title a chapter the way the ebooklib path does."""

import unittest
import zipfile
from io import BytesIO
from unittest.mock import patch

from docreader.parser.parser import Parser


def _archive(body_html: str, head_title: str = "Stored Title") -> bytes:
    output = BytesIO()
    with zipfile.ZipFile(output, "w") as archive:
        archive.writestr(
            "OPS/chapter_01.xhtml",
            "<html><head><title>"
            + head_title
            + "</title></head><body>"
            + body_html
            + "</body></html>",
        )
    return output.getvalue()


def _fallback_content(body_html: str, head_title: str = "Stored Title") -> str:
    with patch(
        "docreader.parser.epub_parser.epub.read_epub",
        side_effect=ValueError("exercise ZIP fallback"),
    ):
        return Parser().parse_file(
            "chapter.epub", "epub", _archive(body_html, head_title)
        ).content


class EPUBFallbackHeadingTests(unittest.TestCase):
    def test_heading_in_the_document_is_the_chapter_title(self):
        """The stored file name must not become a second, wrong heading."""
        content = _fallback_content("<h1>Chapter One</h1><p>Body text.</p>")
        self.assertEqual(content, "## Chapter One\n\nBody text.")

    def test_stored_title_element_is_not_body_text(self):
        content = _fallback_content(
            "<h1>Chapter One</h1><p>Body text.</p>", head_title="Ignored Title"
        )
        self.assertNotIn("Ignored Title", content)

    def test_text_before_the_heading_keeps_its_place(self):
        """A preamble must stay above the heading, as on the ebooklib path."""
        content = _fallback_content(
            "<p>Preamble.</p><h1>Chapter One</h1><p>Body text.</p>"
        )
        self.assertTrue(content.startswith("Preamble."), content)
        self.assertNotIn("## Chapter 01", content)
        self.assertIn("## Chapter One", content)


if __name__ == "__main__":
    unittest.main()
