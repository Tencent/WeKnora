"""An image in a table cell or a heading stays an image in the Markdown."""

import unittest
from email.message import EmailMessage

from docreader.parser.html_parser import HTMLParser
from docreader.parser.mhtml_parser import MHTMLParser

_IMG = '<img src="https://example.com/pen.png" alt="pen photo">'


def _markdown(html: str) -> str:
    return HTMLParser(file_name="images.html", file_type="html").parse_into_text(
        html.encode()
    ).content


class HTMLInlineImageTest(unittest.TestCase):
    def test_image_in_a_table_cell_stays_an_image(self):
        # markdownify wrote only the alt text, so the cell read "pen photo"
        markdown = _markdown(
            "<table><tr><th>Item</th><th>Photo</th></tr>"
            f"<tr><td>pen</td><td>{_IMG}</td></tr></table>"
        )

        self.assertEqual(
            markdown,
            "| Item | Photo |\n| --- | --- |\n"
            "| pen | ![pen photo](https://example.com/pen.png) |",
        )

    def test_image_without_alt_text_is_not_dropped_from_its_cell(self):
        markdown = _markdown(
            "<table><tr><th>Item</th><th>Photo</th></tr><tr><td>pen</td>"
            '<td><img src="https://example.com/pen.png"></td></tr></table>'
        )

        self.assertIn("| pen | ![](https://example.com/pen.png) |", markdown)

    def test_wrapped_image_in_a_table_cell_stays_an_image(self):
        for wrapped, expected in (
            (f"<p>{_IMG}</p>", "![pen photo](https://example.com/pen.png)"),
            (
                f'<a href="https://example.com/pen">{_IMG}</a>',
                "[![pen photo](https://example.com/pen.png)](https://example.com/pen)",
            ),
        ):
            with self.subTest(wrapped=wrapped):
                markdown = _markdown(
                    f"<table><tr><th>Photo</th></tr><tr><td>{wrapped}</td></tr></table>"
                )

                self.assertEqual(markdown, f"| Photo |\n| --- |\n| {expected} |")

    def test_image_in_a_heading_stays_an_image(self):
        markdown = _markdown(f"<h2>Pens {_IMG}</h2>")

        self.assertEqual(markdown, "## Pens ![pen photo](https://example.com/pen.png)")

    def test_pipe_in_the_alt_text_of_a_cell_image_is_escaped(self):
        markdown = _markdown(
            "<table><tr><th>Photo</th><th>Note</th></tr><tr>"
            '<td><img src="https://example.com/pen.png" alt="pen|ink"></td>'
            "<td>new</td></tr></table>"
        )

        self.assertIn("| ![pen\\|ink](https://example.com/pen.png) | new |", markdown)

    def test_archived_image_in_a_table_cell_is_referenced(self):
        message = EmailMessage()
        message.make_related()
        root = EmailMessage()
        root.set_content(
            "<table><tr><th>Item</th><th>Photo</th></tr><tr><td>pen</td>"
            '<td><img src="pen.png" alt="pen photo"></td></tr></table>',
            subtype="html",
        )
        root["Content-Location"] = "https://example.test/docs/page.html"
        image = EmailMessage()
        image.set_content(b"image fixture", maintype="image", subtype="png")
        image["Content-Location"] = "https://example.test/docs/pen.png"
        message.attach(root)
        message.attach(image)

        result = MHTMLParser(file_name="page.mhtml").parse_into_text(
            message.as_bytes()
        )

        self.assertIn("| pen | ![pen photo](images/pen.png) |", result.content)
        self.assertEqual(result.metadata["image_count"], 1)


if __name__ == "__main__":
    unittest.main()
