"""WMF/EMF data URIs are rasterized to PNG before they are stored.

MarkItDown inlines Office drawings as ``data:image/x-wmf`` and
``data:image/x-emf``. Storing those bytes under a ``.x-wmf`` / ``.x-emf``
name makes the multimodal pipeline sniff ``application/octet-stream`` and
archive the task. These tests stub the shared rasterizer so they do not
need ImageMagick.
"""

import base64
import struct
import unittest
from unittest.mock import patch

from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownParser

# 1x1 PNG. The rasterizer stub returns this instead of calling ImageMagick.
PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4"
    "z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)
# Placeable WMF key (MS-WMF 2.3.2.3) plus a payload the stub must receive unchanged.
WMF = struct.pack("<IH", 0x9AC6CDD7, 0) + b"WMF-PAYLOAD"
# EMR_HEADER type and the ' EMF' signature, plus a payload the stub must receive.
EMF = struct.pack("<II", 1, 88) + b" EMF" + b"EMF-PAYLOAD"


def _data_uri(subtype: str, raw: bytes, title: str = "") -> str:
    body = base64.b64encode(raw).decode()
    extra = f" {title}" if title else ""
    return f"![drawing](data:image/{subtype};base64,{body}{extra})"


class MarkdownMetafileImageTests(unittest.TestCase):
    def _extract(self, subtype: str, raw: bytes, title: str = ""):
        seen = []

        def fake_rasterize(name, data):
            seen.append((name, data))
            return PNG

        text = "before " + _data_uri(subtype, raw, title) + " after"
        with patch(
            "docreader.parser.pptx_media.rasterize_media_bytes", fake_rasterize
        ):
            converted, images = MarkdownImageUtil().extract_base64(text)
        return converted, images, seen

    def test_x_wmf_is_stored_as_png(self):
        text, images, seen = self._extract("x-wmf", WMF, '"figure"')
        self.assertEqual(len(images), 1)
        path, data = next(iter(images.items()))
        self.assertTrue(path.endswith(".png"), path)
        self.assertNotIn(".x-wmf", path)
        self.assertEqual(data, PNG)
        self.assertNotIn("data:image", text)
        self.assertIn('"figure"', text)
        self.assertEqual(seen, [("drawing.wmf", WMF)])

    def test_x_emf_is_stored_as_png(self):
        text, images, seen = self._extract("x-emf", EMF)
        self.assertEqual(len(images), 1)
        path, data = next(iter(images.items()))
        self.assertTrue(path.endswith(".png"), path)
        self.assertEqual(data, PNG)
        self.assertNotIn("data:image", text)
        self.assertEqual(seen, [("drawing.emf", EMF)])

    def test_plain_wmf_and_emf_subtypes(self):
        for subtype, raw, suffix in (("wmf", WMF, ".wmf"), ("emf", EMF, ".emf")):
            with self.subTest(subtype=subtype):
                _, images, seen = self._extract(subtype, raw)
                path, data = next(iter(images.items()))
                self.assertTrue(path.endswith(".png"), path)
                self.assertEqual(data, PNG)
                self.assertTrue(seen[0][0].endswith(suffix), seen[0][0])
                self.assertEqual(seen[0][1], raw)

    def test_parser_publishes_png_bytes(self):
        raw = _data_uri("x-wmf", WMF).encode()

        def fake_rasterize(name, data):
            self.assertEqual(data, WMF)
            self.assertTrue(name.endswith(".wmf"), name)
            return PNG

        with patch(
            "docreader.parser.pptx_media.rasterize_media_bytes", fake_rasterize
        ):
            document = MarkdownParser(file_name="drawing.md").parse(raw)
        self.assertEqual(len(document.images), 1)
        path, payload = next(iter(document.images.items()))
        self.assertTrue(path.startswith("images/") and path.endswith(".png"), path)
        self.assertEqual(base64.b64decode(payload), PNG)
        self.assertNotIn("x-wmf", document.content)

    def test_unrasterizable_metafile_is_not_stored(self):
        text = "before " + _data_uri("x-wmf", WMF) + " after"
        with patch(
            "docreader.parser.pptx_media.rasterize_media_bytes", return_value=None
        ):
            converted, images = MarkdownImageUtil().extract_base64(text)
        self.assertEqual(images, {})
        self.assertNotIn("data:image", converted)
        self.assertNotIn("x-wmf", converted)
        self.assertIn("drawing", converted)

    def test_png_is_not_sent_to_the_rasterizer(self):
        text = f"![logo](data:image/png;base64,{base64.b64encode(PNG).decode()})"
        with patch(
            "docreader.parser.pptx_media.rasterize_media_bytes",
            side_effect=AssertionError("png must not be rasterized"),
        ):
            converted, images = MarkdownImageUtil().extract_base64(text)
        self.assertEqual(list(images.values()), [PNG])
        self.assertTrue(next(iter(images)).endswith(".png"))
        self.assertNotIn("data:image", converted)

    def test_fenced_metafile_is_left_alone(self):
        text = "```\n" + _data_uri("x-wmf", WMF) + "\n```"
        with patch(
            "docreader.parser.pptx_media.rasterize_media_bytes",
            side_effect=AssertionError("fenced metafile must not be rasterized"),
        ):
            converted, images = MarkdownImageUtil().extract_base64(text)
        self.assertEqual(converted, text)
        self.assertEqual(images, {})


if __name__ == "__main__":
    unittest.main()
