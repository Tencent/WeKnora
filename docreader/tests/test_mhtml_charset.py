"""Decode archived HTML before converting it to Markdown."""
import unittest
from email.message import EmailMessage

from docreader.parser.mhtml_parser import MHTMLParser


def archive_html(payload, charset=None):
    root = EmailMessage()
    root.make_related()
    part = EmailMessage()
    part.set_content(payload, maintype="text", subtype="html")
    if charset:
        part.set_param("charset", charset)
    root.attach(part)
    return root.as_bytes()


class MHTMLCharsetTests(unittest.TestCase):
    def test_html_declared_charset_without_mime_charset(self):
        for encoding, text in (
            ("gb18030", "中文归档内容"),
            ("big5", "繁體中文內容"),
            ("shift_jis", "日本語の記事"),
            ("utf-8", "Café 中文"),
        ):
            for declaration in (
                f'<meta charset="{encoding}">',
                f'<meta http-equiv="Content-Type" content="text/html; charset={encoding}">',
            ):
                with self.subTest(encoding=encoding, declaration=declaration):
                    html = f"<html><head>{declaration}</head><body><p>{text}</p></body></html>"
                    document = MHTMLParser().parse(archive_html(html.encode(encoding)))
                    self.assertEqual(text, document.content)
                    self.assertEqual("mhtml", document.metadata["source_format"])

    def test_bom_without_mime_charset(self):
        for encoding in ("utf-8-sig", "utf-16"):
            with self.subTest(encoding=encoding):
                html = "<html><body><p>中文内容</p></body></html>"
                document = MHTMLParser().parse(archive_html(html.encode(encoding)))
                self.assertEqual("中文内容", document.content)

    def test_explicit_mime_charset_keeps_precedence(self):
        html = '<html><head><meta charset="utf-8"></head><body><p>Café</p></body></html>'
        document = MHTMLParser().parse(archive_html(html.encode("windows-1252"), "windows-1252"))
        self.assertEqual("Café", document.content)

    def test_undeclared_utf8_and_ascii(self):
        for text in ("Plain article", "中文内容"):
            with self.subTest(text=text):
                document = MHTMLParser().parse(archive_html(f"<p>{text}</p>".encode()))
                self.assertEqual(text, document.content)
