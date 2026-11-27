"""Keep DOCX table cell boundaries intact in MarkItDown's Markdown tables."""

from bs4 import BeautifulSoup
from markitdown.converters import DocxConverter, HtmlConverter

from docreader.parser.mhtml_parser import MHTMLParser


class _EscapingHtmlConverter(HtmlConverter):
    """Escape table cell pipes before markdownify turns the HTML into a table.

    MarkItDown converts a DOCX to HTML with mammoth and then writes the table
    with markdownify, which leaves a ``|`` in a cell's text as it is. The
    reader then takes that pipe for a column delimiter, so every cell after it
    shifts one column right and the row gains a column. MarkItDown's PPTX and
    CSV converters are wrapped in this package for the same reason; a DOCX
    goes through this converter instead.
    """

    def convert_string(self, html_content, **kwargs):
        # Unchanged behaviour (and no parse cost) for documents without a
        # table, which is the common case.
        if "<table" not in html_content:
            return super().convert_string(html_content, **kwargs)
        soup = BeautifulSoup(html_content, "html.parser")
        MHTMLParser._escape_table_cell_pipes(soup)
        return super().convert_string(str(soup), **kwargs)


class EscapedDocxConverter(DocxConverter):
    def __init__(self):
        super().__init__()
        # DocxConverter hands the mammoth HTML to this instance attribute, so
        # replacing it is enough; the conversion itself stays MarkItDown's.
        self._html_converter = _EscapingHtmlConverter()
