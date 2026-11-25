"""The shared copy of a DOCX must not outlive the run that made it."""

import os
import tempfile
import unittest

from docreader.parser.docx_parser import Docx


class SharedCopyCleanupTest(unittest.TestCase):
    def test_a_failed_multiprocess_run_removes_the_shared_copy(self):
        processor = Docx()
        processor.doc = None
        processor.para_page_mapping = {1: []}

        shared = tempfile.NamedTemporaryFile(suffix=".docx", delete=False)
        shared.write(b"PK\x03\x04")
        shared.close()
        self.addCleanup(
            lambda: os.path.exists(shared.name) and os.unlink(shared.name)
        )

        processor._prepare_document_sharing = lambda binary: shared.name

        def die(args_list, max_workers):
            raise RuntimeError("worker died")

        processor._execute_multiprocess_tasks = die

        with self.assertRaises(RuntimeError):
            processor._process_document(b"PK\x03\x04", [1], 0, 1, 2)

        self.assertFalse(
            os.path.exists(shared.name),
            "the shared document copy outlived the run that failed",
        )


if __name__ == "__main__":
    unittest.main()
