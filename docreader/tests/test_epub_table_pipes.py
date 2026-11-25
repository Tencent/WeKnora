"""EPUB table cell text must not introduce extra Markdown columns."""

from io import BytesIO
import unittest
import zipfile

from ebooklib import epub

from docreader.parser.parser import Parser


def _table_epub(*, missing_navigation: bool) -> bytes:
    book = epub.EpubBook()
    book.set_identifier("table-pipes")
    book.set_title("Table pipes")
    book.set_language("en")
    chapter = epub.EpubHtml(title="Table", file_name="text/chapter.xhtml")
    chapter.content = (
        "<html><body><h1>Table</h1>"
        "<p>Outside: a|b.</p><pre>shell | command</pre>"
        "<table><tr><th>Expr | type</th><th>Meaning</th></tr>"
        "<tr><td>a|b</td><td>choice</td></tr>"
        "<tr><td>run <code>ls|wc</code></td>"
        "<td><strong>int&#124;None</strong></td></tr>"
        '<tr><td><a href="https://example.com">left|right</a></td>'
        "<td>pen<!-- hidden|comment --></td></tr>"
        "</table><p>After the table.</p></body></html>"
    )
    book.add_item(chapter)
    book.spine = [chapter]
    book.toc = [chapter]
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())
    output = BytesIO()
    epub.write_epub(output, book)
    if not missing_navigation:
        return output.getvalue()

    # A missing NCX triggers the real EbookLib error/recovery path. The chapter
    # and package remain readable by the ZIP fallback; no converter is mocked.
    recovered = BytesIO()
    with zipfile.ZipFile(output) as original:
        with zipfile.ZipFile(recovered, "w") as archive:
            for entry in original.infolist():
                if not entry.filename.endswith("toc.ncx"):
                    archive.writestr(entry, original.read(entry))
    return recovered.getvalue()


class EPUBTablePipesTest(unittest.TestCase):
    def check_table(self, *, missing_navigation: bool):
        with self.assertLogs("docreader.parser.epub_parser", level="INFO") as logs:
            document = Parser().parse_file(
                "table.epub",
                "epub",
                _table_epub(missing_navigation=missing_navigation),
                parser_engine="builtin",
            )
        self.assertEqual(
            any("trying ZIP fallback" in line for line in logs.output),
            missing_navigation,
        )
        rows = [line for line in document.content.splitlines() if line.startswith("|")]
        self.assertEqual(
            rows,
            [
                "| Expr \\| type | Meaning |",
                "| --- | --- |",
                "| a\\|b | choice |",
                "| run `ls\\|wc` | **int\\|None** |",
                "| [left\\|right](https://example.com) | pen |",
            ],
        )
        self.assertIn("Outside: a|b.", document.content)
        self.assertIn("```\nshell | command\n```", document.content)
        self.assertIn("\n\nAfter the table.", document.content)
        self.assertNotIn("hidden", document.content)
        self.assertEqual(document.images, {})

    def test_ebooklib_keeps_pipes_inside_cells(self):
        self.check_table(missing_navigation=False)

    def test_zip_recovery_keeps_pipes_inside_cells(self):
        self.check_table(missing_navigation=True)


if __name__ == "__main__":
    unittest.main()
