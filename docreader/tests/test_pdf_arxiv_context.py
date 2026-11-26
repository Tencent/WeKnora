"""Do not discard scientific text containing an arXiv reference."""
import unittest

from docreader.parser.pdf_parser import PDFParser, _strip_arxiv_and_page_num_lines
from docreader.tests.test_pdf_text_only_figure import _pdf


class ArxivContextTest(unittest.TestCase):
    def test_keep_inline_id_url_and_following_words(self):
        for line in (
            "Important findings from arXiv:2401.00001 show 95 percent accuracy.",
            "See https://arxiv.org/abs/2401.00001 for benchmark results.",
            "The ARXIV:2401.00001 citation supports this conclusion.",
            "References: arXiv:2401.00001 and arXiv:2402.12345.",
        ):
            with self.subTest(line=line):
                self.assertEqual(_strip_arxiv_and_page_num_lines(line), line)

    def test_existing_header_and_adjacent_page_number_behavior_is_unchanged(self):
        for header in ("arXiv:1512.03385v1 [cs.CV] 10 Dec 2015", "arXiv:math/0201234v2 3 May 2002", "arXiv:2401.00001"):
            with self.subTest(header=header):
                result = _strip_arxiv_and_page_num_lines("Body text.\n1\n" + header + "\nMore body.")
                self.assertEqual(result, "Body text.\nMore body.")

    def test_real_multi_page_pdf_preserves_sentences(self):
        content = _pdf([
            (["Important findings from arXiv:2401.00001 show 95 percent accuracy.",
              "Final row: report ID ABC-123"], False),
            (["See https://arxiv.org/abs/2401.00001 for benchmark results.",
              "Paper arXiv:2402.12345 reports an independent result."], False),
        ])
        doc = PDFParser(file_name="papers.pdf").parse(content)
        for fragment in ("arXiv:2401.00001", "95 percent accuracy", "report ID ABC-123",
                         "https://arxiv.org/abs/2401.00001", "independent result"):
            self.assertIn(fragment, doc.content)
        for fragment, page in (("95 percent accuracy", 1), ("independent result", 2)):
            block = next(b for b in doc.source_blocks if fragment in doc.content[b["start"]:b["end"]])
            self.assertEqual(block["locator"]["page"], page)


if __name__ == "__main__":
    unittest.main()
