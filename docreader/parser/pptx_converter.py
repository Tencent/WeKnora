"""Keep PPTX table cell boundaries intact in MarkItDown's Markdown tables."""

from markitdown.converters import PptxConverter

from docreader.parser.csv_converter import _escape_cell


class EscapedPptxConverter(PptxConverter):
    def _convert_table_to_markdown(self, table, **kwargs):
        # MarkItDown writes cells through an HTML table and leaves "|" as it
        # is, so a cell holding one is read as two columns.
        rows = [[_escape_cell(cell.text) for cell in row.cells] for row in table.rows]
        if not rows:
            return "\n"
        lines = ["| " + " | ".join(row) + " |" for row in rows]
        lines.insert(1, "| " + " | ".join(["---"] * len(rows[0])) + " |")
        return "\n".join(lines) + "\n"
