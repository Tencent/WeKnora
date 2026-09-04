"""Unit tests for OpenDataLoader parser helpers (no JVM required)."""

import os
import sys
import tempfile
import unittest
from unittest import mock

from docreader.parser.opendataloader_parser import (
    OpenDataLoaderParser,
    _collect_images_under_output,
    _find_markdown_file,
    _minimal_warmup_pdf,
    _normalize_odl_image_url,
    _ping_hybrid,
    _run_convert,
    _rewrite_markdown_image_refs,
    opendataloader_available,
    warmup_engine,
)


class OpenDataLoaderHelpersTest(unittest.TestCase):
    def test_hybrid_health_probe_blocks_private_url_before_request(self):
        with mock.patch(
            "docreader.parser.opendataloader_parser.is_ssrf_safe_url",
            return_value=(False, "restricted test address"),
        ), mock.patch("urllib.request.build_opener") as build_opener:
            ok, msg = _ping_hybrid("http://127.0.0.1:8080", retries=1)

        self.assertFalse(ok)
        self.assertIn("SSRF", msg)
        build_opener.assert_not_called()

    def test_convert_blocks_private_hybrid_url_at_final_sink(self):
        fake_module = mock.Mock()
        with mock.patch.dict(sys.modules, {"opendataloader_pdf": fake_module}):
            with self.assertRaisesRegex(RuntimeError, "SSRF"):
                _run_convert(
                    "/tmp/input.pdf",
                    "/tmp/output",
                    "/tmp/output/images",
                    overrides={
                        "odl_hybrid": "docling-fast",
                        "odl_hybrid_url": "http://169.254.169.254/latest/meta-data",
                    },
                )

        fake_module.convert.assert_not_called()

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


class OpenDataLoaderWarmupTest(unittest.TestCase):
    def test_minimal_warmup_pdf_is_spec_shaped(self):
        pdf = _minimal_warmup_pdf()
        self.assertTrue(pdf.startswith(b"%PDF-1.4\n"))
        self.assertTrue(pdf.rstrip().endswith(b"%%EOF"))
        self.assertIn(b"xref", pdf)
        self.assertIn(b"startxref", pdf)
        self.assertIn(b"/Root 1 0 R", pdf)

    def test_minimal_warmup_pdf_parses_with_pypdf(self):
        from io import BytesIO

        from pypdf import PdfReader

        reader = PdfReader(BytesIO(_minimal_warmup_pdf()))
        self.assertEqual(1, len(reader.pages))

    @mock.patch(
        "docreader.parser.opendataloader_parser.opendataloader_available",
        return_value=(False, "需要 Java 11+"),
    )
    def test_warmup_skips_when_engine_unavailable(self, mock_avail):
        with mock.patch(
            "docreader.parser.opendataloader_parser._run_convert"
        ) as mock_convert:
            ok, msg = warmup_engine()

        self.assertFalse(ok)
        self.assertIn("Java", msg)
        mock_convert.assert_not_called()

    @mock.patch(
        "docreader.parser.opendataloader_parser.opendataloader_available",
        return_value=(True, ""),
    )
    @mock.patch("docreader.parser.opendataloader_parser._run_convert")
    def test_warmup_runs_tiny_convert_and_reports_elapsed(
        self, mock_convert, _mock_avail
    ):
        seen = {}

        def fake_convert(pdf_path, output_dir, image_dir, overrides=None):
            with open(pdf_path, "rb") as f:
                seen["pdf"] = f.read()

        mock_convert.side_effect = fake_convert

        ok, msg = warmup_engine()

        self.assertTrue(ok)
        self.assertIn("convert finished", msg)
        self.assertEqual(1, mock_convert.call_count)
        self.assertTrue(mock_convert.call_args[0][0].endswith("warmup.pdf"))
        self.assertTrue(seen["pdf"].startswith(b"%PDF-1.4"))

    @mock.patch(
        "docreader.parser.opendataloader_parser.opendataloader_available",
        return_value=(True, ""),
    )
    @mock.patch(
        "docreader.parser.opendataloader_parser._run_convert",
        side_effect=RuntimeError("JVM exploded"),
    )
    def test_warmup_swallows_convert_failures(self, _mock_convert, _mock_avail):
        ok, msg = warmup_engine()

        self.assertFalse(ok)
        self.assertIn("RuntimeError", msg)
        self.assertIn("JVM exploded", msg)


if __name__ == "__main__":
    unittest.main()
