import io
import unittest
import zipfile
from unittest.mock import patch

from docreader.parser import docx_preview

PNG = b"\x89PNG\r\n\x1a\n" + b"fake-png"


def make_docx(files):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in files.items():
            archive.writestr(name, data)
    return output.getvalue()


def base_files(content_types=None, rels=None, header_rels=None):
    files = {
        "[Content_Types].xml": content_types
        or (
            '<?xml version="1.0" encoding="UTF-8"?>'
            '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-'
            'package.relationships+xml"/>'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Default Extension="emf" ContentType="image/x-emf"/>'
            "</Types>"
        ),
        "word/document.xml": "<w:document/>",
        "word/media/image1.emf": b"emf-one",
        "word/media/image2.wmf": b"wmf-two",
        "word/media/image3.png": b"original-png",
        "word/_rels/document.xml.rels": rels
        or (
            '<?xml version="1.0"?><Relationships>'
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/'
            'officeDocument/2006/relationships/image" Target="media/image1.emf"/>'
            '<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/'
            'officeDocument/2006/relationships/image" Target="media/image2.wmf"/>'
            "</Relationships>"
        ),
    }
    if header_rels is not None:
        files["word/_rels/header1.xml.rels"] = header_rels
    return files


def archive_entries(data):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        return {name: archive.read(name) for name in archive.namelist()}


class FindVectorMediaTest(unittest.TestCase):
    def test_only_media_metafiles_are_reported(self):
        data = make_docx(
            {
                "word/media/image1.emf": b"x",
                "word/media/image2.WMF": b"x",
                "word/media/image3.png": b"x",
                "word/embeddings/ole.emf": b"x",
            }
        )
        self.assertEqual(
            docx_preview.find_vector_media(data),
            ["word/media/image1.emf", "word/media/image2.WMF"],
        )


class ConvertDocxPreviewTest(unittest.TestCase):
    def test_no_vector_media_is_skipped(self):
        data = make_docx({"word/media/image1.png": b"png"})
        result = docx_preview.convert_docx_preview(data, "plain.docx")
        self.assertFalse(result.converted)
        self.assertEqual(result.skipped_reason, "no EMF/WMF images")
        self.assertEqual(result.content, b"")
        self.assertEqual(result.error, "")

    def test_invalid_package_reports_error(self):
        result = docx_preview.convert_docx_preview(b"not-a-zip", "broken.docx")
        self.assertFalse(result.converted)
        self.assertIn("not a valid docx package", result.error)
        self.assertEqual(result.content, b"")

    def test_rewrites_relationships_and_content_types(self):
        files = base_files(
            header_rels=(
                '<?xml version="1.0"?><Relationships>'
                '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/'
                'officeDocument/2006/relationships/image" Target="media/image1.emf"/>'
                "</Relationships>"
            )
        )
        data = make_docx(files)

        def rasterize(name, _data):
            return PNG if name.endswith(".emf") else None

        with patch.object(docx_preview, "rasterize_vector_media", side_effect=rasterize):
            result = docx_preview.convert_docx_preview(data, "manual.docx")

        self.assertTrue(result.converted)
        self.assertEqual(result.error, "")
        self.assertEqual(len(result.warnings), 1)
        self.assertIn("image2.wmf", result.warnings[0])

        entries = archive_entries(result.content)
        self.assertIn("word/media/image1.png", entries)
        self.assertEqual(entries["word/media/image1.png"], PNG)
        self.assertNotIn("word/media/image1.emf", entries)
        # The image that could not be rendered keeps its original bytes and
        # relationship, so the package stays valid.
        self.assertIn("word/media/image2.wmf", entries)
        self.assertEqual(entries["word/media/image2.wmf"], b"wmf-two")
        # Untouched parts are byte-identical.
        self.assertEqual(entries["word/document.xml"], files["word/document.xml"].encode())
        self.assertEqual(entries["word/media/image3.png"], b"original-png")

        document_rels = entries["word/_rels/document.xml.rels"].decode()
        self.assertIn('Target="media/image1.png"', document_rels)
        self.assertIn('Target="media/image2.wmf"', document_rels)
        header_rels = entries["word/_rels/header1.xml.rels"].decode()
        self.assertIn('Target="media/image1.png"', header_rels)

        content_types = entries["[Content_Types].xml"].decode()
        self.assertIn('Extension="png" ContentType="image/png"', content_types)
        self.assertIn('Extension="emf" ContentType="image/x-emf"', content_types)

    def test_drops_content_type_override_for_replaced_part(self):
        content_types = (
            '<?xml version="1.0" encoding="UTF-8"?>'
            '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
            '<Default Extension="png" ContentType="image/png"/>'
            '<Override PartName="/word/media/image1.emf" ContentType="image/x-emf"/>'
            "</Types>"
        )
        data = make_docx(base_files(content_types=content_types))
        with patch.object(docx_preview, "rasterize_vector_media", return_value=PNG):
            result = docx_preview.convert_docx_preview(data, "manual.docx")
        self.assertTrue(result.converted)
        content_types = archive_entries(result.content)["[Content_Types].xml"].decode()
        self.assertNotIn("image1.emf", content_types)
        self.assertEqual(content_types.count('Extension="png"'), 1)

    def test_all_rasterizations_failing_returns_no_copy(self):
        data = make_docx(base_files())
        with patch.object(docx_preview, "rasterize_vector_media", return_value=None):
            result = docx_preview.convert_docx_preview(data, "manual.docx")
        self.assertFalse(result.converted)
        self.assertEqual(result.content, b"")
        self.assertEqual(result.skipped_reason, "no vector image could be rasterized")
        self.assertEqual(len(result.warnings), 2)

    def test_name_collision_gets_a_unique_png(self):
        files = base_files()
        files["word/media/image1.png"] = b"already-there"
        data = make_docx(files)
        with patch.object(docx_preview, "rasterize_vector_media", return_value=PNG):
            result = docx_preview.convert_docx_preview(data, "manual.docx")
        entries = archive_entries(result.content)
        self.assertEqual(entries["word/media/image1.png"], b"already-there")
        self.assertIn("word/media/image1_emf.png", entries)
        rels = entries["word/_rels/document.xml.rels"].decode()
        self.assertIn('Target="media/image1_emf.png"', rels)


if __name__ == "__main__":
    unittest.main()
