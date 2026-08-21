import io
import unittest
from pathlib import Path

from markitdown import MarkItDown

from docreader.parser.markdown_parser import MarkdownTableUtil


class TestMarkdownTableUtil(unittest.TestCase):
    def test_preserves_empty_cells(self):
        """Interior empty cells must not be dropped during formatting."""
        raw = "| a |  | c |\n| --- | --- | --- |\n| 1 | 2 | 3 |"
        formatted = MarkdownTableUtil().format_table(raw)
        self.assertIn("| a |  | c |", formatted)
        self.assertEqual(formatted.count("|"), raw.count("|"))

    def test_format_nonempty_table(self):
        raw = "|Name|Age|\n|---|---|\n|John|30|"
        formatted = MarkdownTableUtil().format_table(raw)
        self.assertIn("| Name | Age |", formatted)
        self.assertIn("| --- | --- |", formatted)
        self.assertIn("| John | 30 |", formatted)

    def test_normalize_markitdown_en_tables(self):
        docx = (
            Path(__file__).resolve().parents[2]
            / "testdata"
            / "rag_test"
            / "docx"
            / "en_tables.docx"
        )
        if not docx.is_file():
            docx = Path(__file__).resolve().parents[2].parent / "testdata/rag_test/docx/en_tables.docx"
        if not docx.is_file():
            self.skipTest("en_tables.docx fixture not available")
        raw = MarkItDown().convert(io.BytesIO(docx.read_bytes()), file_extension=".docx").text_content
        normalized = MarkdownTableUtil().format_table(raw)

        self.assertNotIn("|  |  |  |  |", normalized)
        self.assertIn("| Name | Game | Fame | Blame |", normalized)
        idx_name = normalized.index("| Name | Game | Fame | Blame |")
        idx_sep = normalized.index("| --- | --- | --- | --- |", idx_name)
        self.assertLess(idx_name, idx_sep)
        self.assertIn("| Lebron James | Basketball |", normalized)

        # Headerless 2-row tables: delimiter inserted so GFM renderers show a table
        self.assertIn(
            "| Sinple | Table |\n| --- | --- |\n| Without | Header |", normalized
        )
        self.assertIn(
            "| Simple  Multiparagraph | Table  Full |\n| --- | --- |\n"
            "| Of  Paragraphs | In each  Cell. |",
            normalized,
        )

    def test_malformed_unclosed_row_is_passthrough(self):
        """An unclosed ``|``-prefixed row must not hang the process.

        Regression for Tencent/WeKnora#2768: the old line_pattern explored an
        exponential number of backtracking paths on such rows and stalled the
        single-threaded docreader event loop.
        """
        line = "| " + " | ".join(f"col{i}" for i in range(100)) + "  unclosed"
        formatted = MarkdownTableUtil().format_table(line)
        self.assertEqual(formatted, line)

    def test_long_valid_table_formats(self):
        """A long, well-formed table still formats in linear time."""
        header = "| " + " | ".join(f"c{i}" for i in range(200)) + " |"
        sep = "| " + " | ".join("---" for _ in range(200)) + " |"
        row = "| " + " | ".join(f"v{i}" for i in range(200)) + " |"
        formatted = MarkdownTableUtil().format_table("\n".join([header, sep, row]))
        self.assertIn("| c0 | c1 |", formatted)
        self.assertIn("| v0 | v1 |", formatted)


if __name__ == "__main__":
    unittest.main()
