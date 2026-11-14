import unittest
from email.message import EmailMessage

from docreader.parser.mhtml_parser import MHTMLParser
from docreader.parser.parser import Parser


def _archive(*, start="<root@local>", root_first=False, alternative=False):
    archive = EmailMessage()
    archive.make_related()
    archive.set_param("type", "text/html")
    if start is not None:
        archive.set_param("start", start)

    root = EmailMessage()
    root.set_content(
        '<h1>Selected article</h1><img src="figure.png" alt="figure">',
        subtype="html",
    )
    root["Content-Location"] = "https://example.com/article/index.html"
    if alternative:
        container = EmailMessage()
        container.set_content("Plain text alternative")
        container.make_alternative()
        container.attach(root)
        root = container
        archive.set_param("type", "multipart/alternative")
    root["Content-ID"] = "<root@local>"

    frame = EmailMessage()
    frame.set_content("<h1>Unrelated frame</h1>" + "frame body " * 100, subtype="html")
    frame["Content-ID"] = "<frame@local>"
    frame["Content-Location"] = "https://example.com/frame.html"
    for part in [root, frame] if root_first else [frame, root]:
        archive.attach(part)

    image = EmailMessage()
    image.set_content(b"image fixture", maintype="image", subtype="png")
    image["Content-ID"] = "<image@local>"
    image["Content-Location"] = "https://example.com/article/figure.png"
    archive.attach(image)
    return archive


def _parse(archive):
    return MHTMLParser(file_name="saved.mhtml", file_type="mhtml").parse(
        archive.as_bytes()
    )


class MHTMLRootTest(unittest.TestCase):
    def assert_article(self, document):
        self.assertIn("Selected article", document.content)
        self.assertNotIn("Unrelated frame", document.content)
        self.assertIn("images/figure.png", document.content)
        self.assertEqual(document.metadata["image_count"], 1)

    def test_start_selects_shorter_html_root_in_either_position(self):
        for root_first in (False, True):
            with self.subTest(root_first=root_first):
                self.assert_article(_parse(_archive(root_first=root_first)))

    def test_start_selects_html_inside_alternative_root(self):
        self.assert_article(_parse(_archive(alternative=True)))

    def test_alternative_root_uses_last_supported_html_part(self):
        archive = _archive(alternative=True)
        container = archive.get_payload()[1]
        less_faithful = EmailMessage()
        less_faithful.set_content("<p>Older rendering</p>" * 100, subtype="html")
        container.get_payload().insert(1, less_faithful)
        document = _parse(archive)
        self.assert_article(document)
        self.assertNotIn("Older rendering", document.content)

    def test_start_is_used_by_file_parser_facade(self):
        archive = _archive()
        document = Parser().parse_file("saved.mhtml", "mhtml", archive.as_bytes())
        self.assert_article(document)

    def test_explicit_root_takes_precedence_over_ad_location_heuristic(self):
        archive = _archive()
        root = archive.get_payload()[1]
        root.replace_header("Content-Location", "https://example.com/analytics-report")
        document = _parse(archive)
        self.assertIn("Selected article", document.content)
        self.assertNotIn("Unrelated frame", document.content)

    def test_missing_or_unknown_start_keeps_existing_selection(self):
        for start in (None, "<missing@local>"):
            with self.subTest(start=start):
                document = _parse(_archive(start=start))
                self.assertIn("Unrelated frame", document.content)
                self.assertNotIn("Selected article", document.content)

    def test_non_html_start_keeps_existing_selection(self):
        document = _parse(_archive(start="<image@local>"))
        self.assertIn("Unrelated frame", document.content)

    def test_non_related_container_keeps_existing_selection(self):
        archive = _archive()
        archive.set_type("multipart/mixed")
        document = _parse(archive)
        self.assertIn("Unrelated frame", document.content)

    def test_empty_explicit_html_root_does_not_select_an_unrelated_frame(self):
        archive = _archive()
        archive.get_payload()[1].set_payload("")
        document = _parse(archive)
        self.assertNotIn("Unrelated frame", document.content)
