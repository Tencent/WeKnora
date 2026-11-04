import io
import os
import unittest
from dataclasses import replace
from types import SimpleNamespace
from unittest.mock import patch

from PIL import Image

import docreader.parser.pdf_parser as pdf_parser
from docreader.main import DocReaderServicer
from docreader.proto.docreader_pb2 import ReadConfig, ReadRequest


def _scanned_pdf(page_count=2):
    buffer = io.BytesIO()
    pages = [Image.new("RGB", (64, 64), color) for color in ("white", "black")]
    sequence = (pages * ((page_count // len(pages)) + 1))[:page_count]
    sequence[0].save(
        buffer,
        format="PDF",
        save_all=True,
        append_images=sequence[1:],
    )
    return buffer.getvalue()


class _NoopPreprocessRunner:
    def __init__(self):
        self.limits = SimpleNamespace(max_workers=1)

    def run(self, *args, **kwargs):
        return None

    def close(self):
        pass


class ReadStreamPDFTest(unittest.TestCase):
    def test_metadata_precedes_lazy_scanned_page_rendering(self):
        config = ReadConfig()
        config.parser_engine_overrides["pdf_force_scanned"] = "true"
        request = ReadRequest(
            file_content=_scanned_pdf(2),
            file_name="scan.pdf",
            file_type="pdf",
            config=config,
        )
        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        real_render = pdf_parser._render_page_to_jpeg
        stream = None

        try:
            with patch.object(
                pdf_parser,
                "CONFIG",
                replace(pdf_parser.CONFIG, pdf_render_parallelism=1),
            ):
                with patch.object(
                    pdf_parser,
                    "_render_page_to_jpeg",
                    wraps=real_render,
                ) as render:
                    stream = service.ReadStream(request, None)
                    meta = next(stream).meta
                    self.assertEqual(meta.image_count, 2)
                    self.assertEqual(render.call_count, 0)

                    first = next(stream).image
                    self.assertEqual(first.filename, "scan_page_1.jpg")
                    self.assertTrue(first.image_data.startswith(b"\xff\xd8"))
                    self.assertEqual(render.call_count, 1)

                    second = next(stream).image
                    self.assertEqual(second.filename, "scan_page_2.jpg")
                    self.assertTrue(second.image_data.startswith(b"\xff\xd8"))
                    self.assertEqual(render.call_count, 2)

                    with self.assertRaises(StopIteration):
                        next(stream)
        finally:
            if stream is not None:
                stream.close()
            service.close()

    def test_cancelling_after_metadata_removes_temporary_pdf(self):
        config = ReadConfig()
        config.parser_engine_overrides["pdf_force_scanned"] = "true"
        request = ReadRequest(
            file_content=_scanned_pdf(2),
            file_name="scan.pdf",
            file_type="pdf",
            config=config,
        )
        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        real_write = pdf_parser._write_pdf_temp_file
        temp_paths = []
        stream = None

        def record_temp_path(content):
            path = real_write(content)
            temp_paths.append(path)
            return path

        try:
            with patch.object(
                pdf_parser,
                "CONFIG",
                replace(pdf_parser.CONFIG, pdf_render_parallelism=1),
            ):
                with patch.object(
                    pdf_parser,
                    "_write_pdf_temp_file",
                    side_effect=record_temp_path,
                ):
                    stream = service.ReadStream(request, None)
                    self.assertEqual(next(stream).meta.image_count, 2)
                    self.assertEqual(len(temp_paths), 1)
                    self.assertTrue(os.path.exists(temp_paths[0]))
                    stream.close()
                    self.assertFalse(os.path.exists(temp_paths[0]))
        finally:
            if stream is not None:
                stream.close()
            service.close()


if __name__ == "__main__":
    unittest.main()
