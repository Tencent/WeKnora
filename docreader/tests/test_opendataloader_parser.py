"""Unit tests for OpenDataLoader parser helpers (no JVM required)."""

import os
import tempfile
import unittest
from unittest import mock

from docreader.parser.opendataloader_parser import (
    OpenDataLoaderParser,
    _collect_images_under_output,
    _find_markdown_file,
    _normalize_odl_image_url,
    _rewrite_markdown_image_refs,
    opendataloader_available,
)


class OpenDataLoaderHelpersTest(unittest.TestCase):
    def test_find_markdown_prefers_stem_match(self):
        with tempfile.TemporaryDirectory() as d:
            other = os.path.join(d, "other.md")
            target = os.path.join(d, "paper.md")
            with open(other, "w") as f:
                f.write("x")
            with open(target, "w") as f:
                f.write("# Title")
            self.assertEqual(_find_markdown_file(d, "paper"), target)

    def test_collect_and_rewrite_images(self):
        with tempfile.TemporaryDirectory() as d:
            img_dir = os.path.join(d, "images")
            os.makedirs(img_dir)
            png = os.path.join(img_dir, "fig1.png")
            with open(png, "wb") as f:
                f.write(b"\x89PNG\r\n\x1a\n")
            images = _collect_images_under_output(d)
            self.assertIn("images/fig1.png", images)
            md = "See ![fig](images/fig1.png) and ![alt](./fig1.png)."
            out = _rewrite_markdown_image_refs(md, images)
            self.assertIn("![fig](images/fig1.png)", out)
            self.assertIn("![alt](images/fig1.png)", out)

    def test_rewrite_odl_angle_bracket_and_entity_urls(self):
        images = {"images/imageFile1.png": "e30="}
        for md_in in (
            "![image 1](<images/imageFile1.png>)",
            "![image 1](&lt;images/imageFile1.png&gt;)",
        ):
            out = _rewrite_markdown_image_refs(md_in, images)
            self.assertEqual("![image 1](images/imageFile1.png)", out)

    def test_normalize_odl_image_url(self):
        self.assertEqual(
            _normalize_odl_image_url("&lt;images/imageFile2.png&gt;"),
            "images/imageFile2.png",
        )
        self.assertEqual(
            _normalize_odl_image_url("<images/imageFile2.png>"),
            "images/imageFile2.png",
        )

    def test_rewrite_skips_data_uris(self):
        md = "![x](data:image/png;base64,abc)"
        self.assertEqual(_rewrite_markdown_image_refs(md, {"images/a.png": "e30="}), md)


class OpenDataLoaderParserTest(unittest.TestCase):
    @mock.patch("docreader.parser.opendataloader_parser.opendataloader_available")
    @mock.patch("docreader.parser.opendataloader_parser._run_convert")
    def test_parse_reads_markdown_and_images(self, mock_convert, mock_avail):
        mock_avail.return_value = (True, "")

        def fake_convert(pdf_path, output_dir, image_dir, overrides=None):
            stem = os.path.splitext(os.path.basename(pdf_path))[0]
            md_path = os.path.join(output_dir, f"{stem}.md")
            with open(md_path, "w") as f:
                f.write("# Hello\n\n![pic](images/pic.png)\n")
            os.makedirs(image_dir, exist_ok=True)
            with open(os.path.join(image_dir, "pic.png"), "wb") as f:
                f.write(b"png")

        mock_convert.side_effect = fake_convert

        parser = OpenDataLoaderParser(file_name="doc.pdf", file_type="pdf")
        doc = parser.parse_into_text(b"%PDF-1.4 fake")
        self.assertIn("# Hello", doc.content)
        self.assertIn("images/pic.png", doc.content)
        self.assertIn("images/pic.png", doc.images)
        self.assertEqual(doc.metadata.get("parser_engine"), "opendataloader")

    @mock.patch("docreader.parser.opendataloader_parser.shutil.which", return_value=None)
    def test_availability_requires_java(self, _which):
        with mock.patch(
            "docreader.parser.opendataloader_parser._package_available",
            return_value=(True, ""),
        ):
            ok, msg = opendataloader_available()
        self.assertFalse(ok)
        self.assertIn("Java", msg)


if __name__ == "__main__":
    unittest.main()
