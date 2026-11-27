# -*- coding: utf-8 -*-
"""Tests for the AsciiDoc parser.

The fallback-converter tests run everywhere (no external dependencies).
The asciidoctor-binary and end-to-end tests are skipped when the binary
or the HTML toolchain is not installed — they run in the docreader image.
"""

import os
import unittest

from docreader.parser.asciidoc_parser import (
    AsciiDocParser,
    _fallback_adoc_to_markdown,
    _inline,
)

SAMPLE = """= WeKnora AsciiDoc Guide
:toc: left
:author: Team

An intro paragraph with *bold*, _italic_ and a link:https://example.com[link].

== Installation

. Step one
. Step two

* bullet A
** nested bullet
* bullet B

.Step title
[source,python]
----
print("hello")
----

|===
| Name | Value
| CPU | 4
| RAM | 8 GB
|===

image::https://example.com/diagram.png[Architecture diagram]

[WARNING]
--
Be careful.
--
"""


class FallbackConverterTests(unittest.TestCase):
    def test_headings(self):
        md = _fallback_adoc_to_markdown(SAMPLE)
        self.assertIn("# WeKnora AsciiDoc Guide", md)
        self.assertIn("## Installation", md)
        self.assertNotIn(":toc:", md)
        self.assertNotIn(":author:", md)

    def test_inline_formatting(self):
        self.assertEqual(_inline("*bold*"), "**bold**")
        self.assertEqual(_inline("_italic_"), "*italic*")
        self.assertEqual(
            _inline("a link:https://example.com[x]"), "a [x](https://example.com)"
        )
        self.assertEqual(
            _inline("image::https://e.com/a.png[Alt,600]"), "![Alt](https://e.com/a.png)"
        )

    def test_lists(self):
        md = _fallback_adoc_to_markdown(SAMPLE)
        self.assertIn("1. Step one", md)
        self.assertIn("- bullet A", md)
        self.assertIn("  - nested bullet", md)

    def test_source_block(self):
        md = _fallback_adoc_to_markdown(SAMPLE)
        self.assertIn("```", md)
        self.assertIn('print("hello")', md)

    def test_table(self):
        md = _fallback_adoc_to_markdown(SAMPLE)
        self.assertIn("| Name | Value |", md)
        self.assertIn("| CPU | 4 |", md)
        self.assertIn("| --- | --- |", md)

    def test_image_macro(self):
        md = _fallback_adoc_to_markdown(SAMPLE)
        self.assertIn("![Architecture diagram](https://example.com/diagram.png)", md)

    def test_comment_block_dropped(self):
        md = _fallback_adoc_to_markdown("para\n////\nsecret\n////\nafter")
        self.assertNotIn("secret", md)
        self.assertIn("para", md)
        self.assertIn("after", md)

    def test_quote_block(self):
        md = _fallback_adoc_to_markdown("____\nquoted line\n____")
        self.assertIn("> quoted line", md)

    def test_setext_heading(self):
        md = _fallback_adoc_to_markdown("My Title\n========")
        self.assertIn("# My Title", md)

    def test_empty(self):
        self.assertEqual(_fallback_adoc_to_markdown(""), "\n")

    def test_block_title(self):
        md = _fallback_adoc_to_markdown(".My caption\nsome text")
        self.assertIn("**My caption**", md)


class ParserTests(unittest.TestCase):
    """End-to-end over the full pipeline. Skipped without the toolchain."""

    @classmethod
    def setUpClass(cls):
        cls.binary_available = bool(
            os.environ.get("ASCIIDOCTOR_PATH")
            or os.path.exists("/usr/bin/asciidoctor")
            or os.system("command -v asciidoctor >/dev/null 2>&1") == 0
        )

    def test_parse_sample(self):
        try:
            import bs4  # noqa: F401
            import trafilatura  # noqa: F401
        except ImportError:
            self.skipTest("HTML toolchain (bs4/trafilatura) not installed")
        parser = AsciiDocParser(file_name="sample.adoc", file_type="adoc")
        document = parser.parse_into_text(SAMPLE.encode("utf-8"))
        self.assertTrue(document.is_valid())
        self.assertIn("#", document.content)
        self.assertIn("Installation", document.content)

    def test_registry_routes_adoc(self):
        try:
            from docreader.parser.registry import registry
        except Exception as exc:  # py<3.10 cannot import the full parser suite
            self.skipTest(f"registry import failed: {exc}")
        from docreader.parser.base_parser import BaseParser

        cls = registry.get_parser_class("", "adoc")
        self.assertIs(cls, AsciiDocParser)
        self.assertTrue(issubclass(cls, BaseParser))

    def test_fallback_used_without_binary(self):
        if self.binary_available and os.environ.get("ASCIIDOCTOR_PATH") != "/nonexistent":
            # simulate a missing binary to exercise the fallback path
            os.environ["ASCIIDOCTOR_PATH"] = "/nonexistent"
            try:
                parser = AsciiDocParser(file_name="s.adoc", file_type="adoc")
                document = parser.parse_into_text(b"= T\n\nbody")
                self.assertIn("# T", document.content)
            finally:
                os.environ.pop("ASCIIDOCTOR_PATH", None)
        else:
            parser = AsciiDocParser(file_name="s.adoc", file_type="adoc")
            document = parser.parse_into_text(b"= T\n\nbody")
            self.assertIn("# T", document.content)


if __name__ == "__main__":
    unittest.main()
