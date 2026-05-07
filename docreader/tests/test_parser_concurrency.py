import base64
import io
import threading
import time
import unittest
import uuid

from PIL import Image

from docreader.parser.concurrency import parser_worker_limit
from docreader.parser.pdf_parser import PDFScannedParser, _normalize_image_quality


class ParserConcurrencyTest(unittest.TestCase):
    def test_parser_worker_limit_serializes_work(self):
        limiter_name = f"test-{uuid.uuid4()}"
        active_workers = 0
        max_active_workers = 0
        state_lock = threading.Lock()
        start = threading.Barrier(3)

        def worker():
            nonlocal active_workers, max_active_workers
            start.wait()
            with parser_worker_limit(limiter_name, 1):
                with state_lock:
                    active_workers += 1
                    max_active_workers = max(max_active_workers, active_workers)
                time.sleep(0.02)
                with state_lock:
                    active_workers -= 1

        threads = [threading.Thread(target=worker) for _ in range(2)]
        for thread in threads:
            thread.start()

        start.wait()
        for thread in threads:
            thread.join()

        self.assertEqual(max_active_workers, 1)

    def test_scanned_pdf_parser_outputs_jpeg_images(self):
        pdf_bytes = io.BytesIO()
        pages = [
            Image.new("RGB", (64, 64), "white"),
            Image.new("RGB", (64, 64), "black"),
        ]
        pages[0].save(
            pdf_bytes,
            format="PDF",
            save_all=True,
            append_images=pages[1:],
        )

        document = PDFScannedParser(file_name="scan.pdf").parse_into_text(
            pdf_bytes.getvalue()
        )

        image_ref = "images/scan_page_1.jpg"
        self.assertIn(f"![scan_page_1.jpg]({image_ref})", document.content)
        self.assertIn(image_ref, document.images)
        self.assertEqual(document.metadata["image_source_type"], "scanned_pdf")
        self.assertEqual(document.metadata["page_count"], 2)
        self.assertEqual(len(document.images), 2)
        self.assertIn("images/scan_page_2.jpg", document.images)
        image_bytes = base64.b64decode(document.images[image_ref])
        self.assertTrue(image_bytes.startswith(b"\xff\xd8"))

    def test_scanned_pdf_parser_logs_malformed_pdf_without_format_error(self):
        parser = PDFScannedParser(file_name="broken.pdf")

        with self.assertLogs("docreader.parser.pdf_parser", level="ERROR") as logs:
            with self.assertRaises(Exception):
                parser.parse_into_text(b"not a pdf")

        self.assertTrue(
            any("PDFScannedParser failed to parse PDF:" in line for line in logs.output)
        )

    def test_normalize_image_quality_bounds_jpeg_quality(self):
        self.assertEqual(_normalize_image_quality(-1), 1)
        self.assertEqual(_normalize_image_quality(90), 90)
        self.assertEqual(_normalize_image_quality(120), 95)


if __name__ == "__main__":
    unittest.main()
