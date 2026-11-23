import logging
import os
import unittest

from PIL import Image

from docreader.parser.docx_parser import (
    Docx,
    _is_temp_image_file,
    _save_image_to_temp,
)


class TempImageCleanupTest(unittest.TestCase):
    def test_an_image_saved_for_a_worker_is_collected_and_removed(self):
        logger = logging.getLogger("docx-temp-image-test")
        path = _save_image_to_temp(logger, Image.new("RGB", (4, 4)), 1, 0)
        self.assertIsNotNone(path)
        self.addCleanup(lambda: os.path.exists(path) and os.unlink(path))
        self.assertTrue(os.path.exists(path))

        # The collector recognised temporary images by the literal POSIX
        # prefix "/tmp/docx_img_", which never matches a Windows temp path.
        self.assertTrue(_is_temp_image_file(path))

        Docx()._cleanup_temp_image_files({path})

        self.assertFalse(os.path.exists(path))
        self.assertFalse(os.path.exists(os.path.dirname(path)))

    def test_a_path_outside_the_image_temp_dirs_is_not_collected(self):
        self.assertFalse(_is_temp_image_file(None))
        self.assertFalse(_is_temp_image_file(""))
        self.assertFalse(_is_temp_image_file(os.path.join("some", "dir", "a.png")))


if __name__ == "__main__":
    unittest.main()
