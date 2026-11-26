"""MIME related root selection for archives with no explicit start."""
import unittest
from email.message import EmailMessage
from unittest.mock import patch

from docreader.parser.mhtml_parser import MHTMLParser


def archive(*, root_kind="html", include_start=False, related=True):
    message = EmailMessage()
    message.make_related() if related else message.make_mixed()
    message.set_param("type", "multipart/alternative" if root_kind == "alternative" else "text/html")
    if include_start:
        message.set_param("start", "<frame>")
    root = EmailMessage()
    root.set_content("<h1>Actual article</h1>", subtype="html")
    root["Content-ID"] = "<root>"
    if root_kind == "alternative":
        alt = EmailMessage()
        alt.set_content("Plain alternative")
        alt.make_alternative()
        alt.attach(root)
        root = alt
    frame = EmailMessage()
    frame.set_content("<h1>Frame content</h1>" + "long frame " * 100, subtype="html")
    frame["Content-ID"] = "<frame>"
    message.attach(root)
    message.attach(frame)
    return message


def parse(message):
    # Only markdown rendering is stubbed: MIME serialization, decoding and
    # selection use the real parser implementation.
    with patch.object(MHTMLParser, "html_to_markdown", side_effect=lambda html, **kwargs: html):
        return MHTMLParser(file_name="page.mhtml").parse_into_text(message.as_bytes())


class MHTMLImplicitRootTest(unittest.TestCase):
    def test_first_html_is_root_even_if_later_frame_is_larger(self):
        self.assertIn("Actual article", parse(archive()).content)
        self.assertNotIn("Frame content", parse(archive()).content)

    def test_real_markdown_render_preserves_article_and_embedded_image(self):
        msg = archive()
        root = msg.get_payload()[0]
        root.set_content('<h1>Actual article</h1><p>Root body</p><img src="figure.png" alt="Figure">', subtype="html")
        root["Content-Location"] = "https://example.test/docs/page.html"
        image = EmailMessage()
        image.set_content(b"image fixture", maintype="image", subtype="png")
        image["Content-Location"] = "https://example.test/docs/figure.png"
        msg.attach(image)

        # No mock: exercise MIME parsing, root selection, image alias lookup,
        # BeautifulSoup and markdownify together.
        result = MHTMLParser(file_name="page.mhtml").parse_into_text(msg.as_bytes())
        self.assertIn("# Actual article", result.content)
        self.assertIn("Root body", result.content)
        self.assertIn("![Figure](images/figure.png)", result.content)
        self.assertNotIn("Frame content", result.content)
        self.assertEqual(result.metadata["image_count"], 1)

    def test_first_alternative_uses_html(self):
        self.assertIn("Actual article", parse(archive(root_kind="alternative")).content)

    def test_explicit_start_and_non_related_still_keep_prior_behavior(self):
        self.assertIn("Frame content", parse(archive(include_start=True)).content)
        self.assertIn("Frame content", parse(archive(related=False)).content)

    def test_missing_or_mismatched_type_falls_back(self):
        for declared in (None, "image/png"):
            msg = archive()
            if declared is None:
                msg.del_param("type")
            else:
                msg.set_param("type", declared)
            self.assertIn("Frame content", parse(msg).content)

    def test_selected_root_location_is_used_for_image_resolution(self):
        msg = archive()
        msg.get_payload()[0]["Content-Location"] = "https://example.test/docs/page.html"
        seen = []

        def render(html, **kwargs):
            seen.append(kwargs["base_location"])
            return html

        with patch.object(MHTMLParser, "html_to_markdown", side_effect=render):
            MHTMLParser(file_name="page.mhtml").parse_into_text(msg.as_bytes())
        self.assertEqual(seen, ["https://example.test/docs/page.html"])

    def test_unresolvable_explicit_start_keeps_existing_fallback(self):
        msg = archive()
        msg.set_param("start", "<missing>")
        self.assertIn("Frame content", parse(msg).content)

    def test_empty_html_root_does_not_fall_back_to_frame(self):
        msg = archive()
        msg.get_payload()[0].set_payload("")
        self.assertNotIn("Frame content", parse(msg).content)


if __name__ == "__main__":
    unittest.main()
