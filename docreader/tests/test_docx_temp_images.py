"""The page images a DOCX parse writes to disk are cleaned up after the parse."""

import logging
import os
import unittest

from PIL import Image

from docreader.parser.docx_parser import Docx, _save_image_to_temp

LOGGER = logging.getLogger(__name__)


class DocxTempImageCleanupTest(unittest.TestCase):
    def test_cleanup_removes_the_temp_directory_with_the_image(self):
        path = _save_image_to_temp(LOGGER, Image.new("RGB", (4, 4), "white"), 1, 0)
        directory = os.path.dirname(path)
        # Cleanups run last-registered-first: unlink the image before its directory.
        self.addCleanup(lambda: os.path.isdir(directory) and os.rmdir(directory))
        self.addCleanup(lambda: os.path.exists(path) and os.unlink(path))

        Docx()._cleanup_temp_image_files({path})

        self.assertFalse(os.path.exists(path))
        self.assertFalse(os.path.exists(directory))
