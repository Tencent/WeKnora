"""Word gaps must be measured on the glyph advance, not on the ink box."""

import unittest

from docreader.parser.pdf_parser import PDFParser


def _helvetica_page(line: str) -> bytes:
    """A single 12pt Helvetica line on a page wide enough to hold it.

    Total word count is what matters: the parser rebuilds the line glyph by
    glyph, so a wrong gap threshold shows up as a space injected inside a
    token rather than as a missing word.
    """
    content = f"BT /F1 12 Tf 50 750 Td ({line}) Tj ET".encode()
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        (
            b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] "
            b"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>"
        ),
        (
            f"<< /Length {len(content)} >>\nstream\n".encode()
            + content
            + b"\nendstream"
        ),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ]
    out = bytearray(b"%PDF-1.4\n")
    offsets = []
    for number, body in enumerate(objects, 1):
        offsets.append(len(out))
        out += f"{number} 0 obj\n".encode() + body + b"\nendobj\n"
    xref = len(out)
    out += f"xref\n0 {len(offsets) + 1}\n0000000000 65535 f \n".encode()
    for offset in offsets:
        out += f"{offset:010} 00000 n \n".encode()
    out += (
        f"trailer\n<< /Size {len(offsets) + 1} /Root 1 0 R >>\n"
        f"startxref\n{xref}\n%%EOF\n"
    ).encode()
    return bytes(out)


class WordGapTest(unittest.TestCase):
    def test_narrow_glyphs_do_not_split_their_token(self):
        # I, l, i and 1 keep most of their advance as white space, so an
        # ink-box gap measurement sees a word boundary where the line has none.
        for line in (
            "Final row: report ID ABC-123",
            "Visible text on the page",
            "ID",
            "a lilliputian ID",
        ):
            with self.subTest(line=line):
                doc = PDFParser(file_name="words.pdf").parse_into_text(_helvetica_page(line))
                self.assertNotIn("I D", doc.content)
                self.assertIn(line, doc.content)


if __name__ == "__main__":
    unittest.main()
