import base64
import unittest
from io import BytesIO

from PIL import Image

from docreader.main import _resolve_images
from docreader.parser.parser import Parser


class StandaloneImageNameTests(unittest.TestCase):
    def test_special_names_emit_a_safe_reference_and_literal_alt(self):
        data = BytesIO()
        Image.new("RGB", (2, 2), "red").save(data, format="PNG")
        raw = data.getvalue()
        for name, expected_alt in [
            ("scan) final.png", "scan) final.png"),
            ("scan(unclosed.png", "scan(unclosed.png"),
            ("a](b.png", r"a\](b.png"),
            ("a\\b.png", r"a\\b.png"),
            ("a\nnext.png", "a next.png"),
            ("a<angle>.png", r"a\<angle\>.png"),
        ]:
            with self.subTest(name=name):
                document = Parser().parse_file(name, "png", raw)
                self.assertEqual(len(document.images), 1)
                ref = next(iter(document.images))
                self.assertRegex(ref, r"^images/[A-Za-z0-9._/%-]+\.png$")
                self.assertTrue(document.content.endswith(f"]({ref})"))
                self.assertNotIn("\n", document.content)
                self.assertEqual(base64.b64decode(document.images[ref]), raw)
                _, images = _resolve_images(document.images, "name-regression")
                self.assertEqual(images[0].original_ref, ref)
                self.assertEqual(images[0].image_data, raw)
                self.assertEqual(images[0].mime_type, "image/png")
                self.assertEqual(document.content, f"![{expected_alt}]({ref})")

    def test_simple_names_keep_their_existing_reference(self):
        for name, file_type in [
            ("scan.png", "png"),
            ("照片.jpeg", "jpeg"),
            ("my_scan.png", "png"),
        ]:
            with self.subTest(name=name):
                document = Parser().parse_file(name, file_type, b"original bytes")
                self.assertTrue(document.content.endswith(f"](images/{name})"))
                self.assertEqual(
                    base64.b64decode(document.images[f"images/{name}"]),
                    b"original bytes",
                )
