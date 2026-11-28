"""Keep CSV field boundaries intact in MarkItDown's Markdown tables."""

import csv
import io

from charset_normalizer import from_bytes
from markitdown import DocumentConverterResult
from markitdown.converters import CsvConverter


def _escape_cell(value: str) -> str:
    return (
        value.replace("\\", "\\\\")
        .replace("|", "\\|")
        .replace("\r\n", "<br />")
        .replace("\n", "<br />")
        .replace("\r", "<br />")
    )


class EscapedCSVConverter(CsvConverter):
    def convert(self, file_stream, stream_info, **kwargs):
        # Keep MarkItDown's charset selection and header-width policy. Escape
        # individual parsed fields before joining them into Markdown rows.
        data = file_stream.read()
        content = (
            data.decode(stream_info.charset)
            if stream_info.charset
            else str(from_bytes(data).best())
        )
        # A BOM survives the decode and would end up inside the first header
        # cell; strip it before the table is built.
        rows = list(csv.reader(io.StringIO(content.lstrip("\ufeff"))))
        # Exported sheets often start with a blank line. Taking that line as the
        # header leaves a width of 0, which turns every cell of the table into an
        # empty string and drops the whole document.
        while rows and not any(cell.strip() for cell in rows[0]):
            rows.pop(0)
        if not rows:
            return DocumentConverterResult(markdown="")
        width = len(rows[0])
        output = [
            "| " + " | ".join(_escape_cell(c) for c in rows[0]) + " |",
            "| " + " | ".join(["---"] * width) + " |",
        ]
        for row in rows[1:]:
            cells = row[:width] + [""] * max(0, width - len(row))
            output.append("| " + " | ".join(_escape_cell(c) for c in cells) + " |")
        return DocumentConverterResult(markdown="\n".join(output))
