import unittest
from io import BytesIO

from ebooklib import epub

from docreader.parser.parser import Parser


def _parse(body):
    book = epub.EpubBook()
    book.set_identifier("x")
    book.set_title("Book")
    book.set_language("en")
    chapter = epub.EpubHtml(title="Ch", file_name="text/ch1.xhtml")
    chapter.content = f"<html><head><title>Ch</title></head><body>{body}</body></html>"
    book.add_item(chapter)
    book.spine = [chapter]
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    out = BytesIO()
    epub.write_epub(out, book)
    return Parser().parse_file("b.epub", "epub", out.getvalue(), "builtin").content


class EPUBPreambleOrderTests(unittest.TestCase):
    def test_text_before_the_first_heading_stays_before_it(self):
        content = _parse(
            "<p>Intro paragraph.</p><h1>Section A</h1><p>Body of A.</p>"
            "<h2>Section B</h2><p>Body of B.</p>"
        )
        self.assertLess(content.index("Intro paragraph."), content.index("## Section A"))
        self.assertLess(content.index("## Section A"), content.index("Body of A."))
        self.assertEqual(content.count("Section A"), 1)

    def test_leading_heading_is_still_the_chapter_title(self):
        content = _parse("<h1>Section A</h1><p>Body of A.</p>")
        self.assertIn("## Section A\n\nBody of A.", content)


if __name__ == "__main__":
    unittest.main()
