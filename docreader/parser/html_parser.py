"""Static HTML file parser."""

from bs4 import BeautifulSoup

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.mhtml_parser import MHTMLParser


class HTMLToMarkdownParser(BaseParser):
    """Convert uploaded HTML bytes to Markdown without browser or network access."""

    def parse_into_text(self, content: bytes) -> Document:
        if not content.strip():
            return Document()

        # Inspect the original bytes so BOMs and HTML charset declarations are
        # honored before the shared Markdown conversion runs.
        html = BeautifulSoup(content, "lxml").decode()
        markdown = MHTMLParser(
            file_name=self.file_name,
            file_type=self.file_type,
            extract_images=False,
        ).html_to_markdown(
            html,
            strip_internal_links=False,
            fallback_to_raw_html=False,
        )
        return Document(content=markdown or "")


class HTMLParser(PipelineParser):
    """Extract static HTML content and normalize the resulting Markdown."""

    _parser_cls = (HTMLToMarkdownParser, MarkdownParser)
