"""Recover text from the HTML declaration when MIME names an unknown charset."""

import base64
import unittest

from docreader.parser.mhtml_parser import MHTMLParser


class MHTMLUnknownCharsetTests(unittest.TestCase):
    def test_unknown_mime_charset_uses_html_declaration(self):
        payload = '<meta charset="windows-1252"><p>Le café est ouvert.</p>'.encode("cp1252")
        message = (
            b"MIME-Version: 1.0\r\n"
            b"Content-Type: text/html; charset=x-unknown\r\n"
            b"Content-Transfer-Encoding: base64\r\n\r\n"
            + base64.b64encode(payload)
        )
        document = MHTMLParser().parse(message)
        self.assertIn("Le café est ouvert.", document.content)
