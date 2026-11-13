"""PowerPoint normalization should trust the file format over its extension."""

import unittest
from io import BytesIO
from pathlib import Path
from unittest.mock import patch

from pptx import Presentation

from docreader.parser.excel_convert import find_soffice
from docreader.parser.parser import Parser
from docreader.parser.ppt_convert import normalize_ppt_bytes

LEGACY_PPT = (
    Path(__file__).resolve().parents[2]
    / "internal/infrastructure/docparser/anydoc/testdata/upstream/ppt/pres.ppt"
)


def _pptx_bytes():
    presentation = Presentation()
    slide = presentation.slides.add_slide(presentation.slide_layouts[0])
    slide.shapes.title.text = "Converted presentation"
    output = BytesIO()
    presentation.save(output)
    return output.getvalue()


class PowerPointFormatMismatchTests(unittest.TestCase):
    def test_renamed_binary_ppt_reaches_conversion_with_a_legacy_suffix(self):
        legacy = LEGACY_PPT.read_bytes()
        converted = _pptx_bytes()
        for engine in ("", "builtin", "markitdown"):
            with self.subTest(engine=engine):
                # Replace only the external process boundary; use the real
                # registry, normalization, MarkItDown and Markdown pipeline.
                with patch(
                    "docreader.parser.ppt_convert.convert_ppt_to_pptx_bytes",
                    return_value=converted,
                ) as converter:
                    doc = Parser().parse_file(
                        "renamed.pptx", "pptx", legacy, parser_engine=engine
                    )
                converter.assert_called_once_with(legacy, suffix=".ppt")
                self.assertIn("Converted presentation", doc.content)

    def test_missing_libreoffice_reports_the_legacy_format_requirement(self):
        with (
            patch("docreader.parser.ppt_convert.find_soffice", return_value=None),
            self.assertRaisesRegex(ValueError, "LibreOffice is required"),
        ):
            Parser().parse_file("renamed.pptx", "pptx", LEGACY_PPT.read_bytes())

    def test_modern_pptx_does_not_invoke_conversion_even_when_named_ppt(self):
        modern = _pptx_bytes()
        for extension in ("ppt", "pptx"):
            with self.subTest(extension=extension):
                with patch(
                    "docreader.parser.ppt_convert.convert_ppt_to_pptx_bytes",
                    side_effect=AssertionError("modern PPTX must not be converted"),
                ):
                    doc = Parser().parse_file("modern." + extension, extension, modern)
                self.assertIn("Converted presentation", doc.content)

    def test_unknown_pptx_payload_retains_the_existing_passthrough(self):
        content = b"not an OLE or ZIP presentation"
        with patch(
            "docreader.parser.ppt_convert.convert_ppt_to_pptx_bytes",
            side_effect=AssertionError("unknown PPTX must not be converted"),
        ):
            self.assertEqual(normalize_ppt_bytes(content, "pptx"), (content, ".pptx"))

    def test_real_libreoffice_parses_a_renamed_legacy_presentation(self):
        if not find_soffice():
            self.skipTest("LibreOffice is not installed")
        legacy = LEGACY_PPT.read_bytes()
        original = Parser().parse_file("original.ppt", "ppt", legacy)
        renamed = Parser().parse_file("renamed.pptx", "pptx", legacy)
        self.assertIn("Deck Title Slide", renamed.content)
        self.assertIn("Numbers Slide", renamed.content)
        self.assertIn("North", renamed.content)
        self.assertIn("42", renamed.content)
        self.assertEqual(renamed.content, original.content)


if __name__ == "__main__":
    unittest.main()
