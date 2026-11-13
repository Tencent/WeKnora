"""EPUB metadata counts chapter documents independently of section headings."""

from io import BytesIO
import unittest

from ebooklib import epub

from docreader.parser.epub_parser import EPUBParser


class EPUBChapterCountTests(unittest.TestCase):
    def test_section_heading_does_not_increase_chapter_count(self):
        book = epub.EpubBook()
        book.set_identifier("chapter-count")
        book.set_title("Chapter count")
        book.set_language("en")
        chapter = epub.EpubHtml(title="Chapter", file_name="chapter.xhtml")
        chapter.content = "<h1>Chapter</h1><p>Intro.</p><h2>Section</h2><p>Body.</p>"
        book.add_item(chapter)
        book.add_item(epub.EpubNcx())
        book.spine = [chapter]
        output = BytesIO()
        epub.write_epub(output, book)

        document = EPUBParser().parse(output.getvalue())
        self.assertIn("## Section", document.content)
        self.assertEqual(document.metadata["chapter_count"], 1)
