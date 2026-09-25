import json
import unittest

from docreader.models.document import Document
from docreader.parser.source_locator import (
    PageGeometry,
    locate_lines,
    page_blocks,
)


def _ln(text, x0, y0, x1, y1):
    return {"text": text, "bbox": (x0, y0, x1, y1)}


class LocateLinesTest(unittest.TestCase):
    def test_finds_lines_in_order_and_pulls_in_leading_markup(self):
        text = "## 标题\n【版权声明】\n正文 第一行\n正文 第一行"
        hits = locate_lines(text, ["标题", "版权声明】", "正文第一行", "正文 第一行", "无此行"])
        self.assertEqual(hits[0], 0)
        self.assertEqual(text[hits[1]:hits[1] + 2], "【版")
        self.assertEqual(hits[2], text.index("正文"))
        self.assertEqual(hits[3], text.rindex("正文"))
        self.assertIsNone(hits[4])


class PageGeometryTest(unittest.TestCase):
    def test_flips_to_top_left_origin(self):
        g = PageGeometry((0, 0, 200, 100))
        self.assertEqual(g.bbox((20, 80, 100, 90)), [0.1, 0.1, 0.5, 0.2])

    def test_applies_cropbox_offset_and_rotation(self):
        g = PageGeometry((100, 100, 300, 200), rotation=90)
        # Unrotated [0.1, 0.1, 0.5, 0.2] turns clockwise.
        self.assertEqual(g.bbox((120, 180, 200, 190)), [0.8, 0.1, 0.9, 0.5])


class PageBlocksTest(unittest.TestCase):
    def test_groups_lines_into_boxed_paragraphs(self):
        page = (
            "8.14.11 索夹与吊索施工应符合下列规定：\n"
            "1 在满足施工需要的前提下，应减小猫道面层开孔面积，并应在开孔位置四周绑\n"
            "扎防滑木条，设立警示标志。\n"
            "2 索夹在主缆上定位后，应紧固螺栓。"
        )
        lines = [
            _ln("8.14.11 索夹与吊索施工应符合下列规定：", 50, 700, 300, 712),
            _ln("1 在满足施工需要的前提下，应减小猫道面层开孔面积，并应在开孔位置四周绑", 60, 684, 540, 696),
            _ln("扎防滑木条，设立警示标志。", 50, 668, 200, 680),
            _ln("2 索夹在主缆上定位后，应紧固螺栓。", 60, 652, 400, 664),
        ]
        blocks = page_blocks(page, 10, 57, lines, PageGeometry((0, 0, 600, 800)))
        self.assertEqual(len(blocks), 3)
        self.assertEqual(blocks[0]["start"], 10)
        item1 = blocks[1]
        self.assertEqual(page[item1["start"] - 10:item1["end"] - 10].split("\n")[0][:1], "1")
        self.assertIn("设立警示标志。", page[item1["start"] - 10:item1["end"] - 10])
        self.assertEqual(item1["locator"]["page"], 57)
        x0, y0, x1, y1 = item1["locator"]["bbox"]
        self.assertAlmostEqual(y0, 1 - 696 / 800, places=3)
        self.assertAlmostEqual(y1, 1 - 668 / 800, places=3)
        self.assertEqual(blocks[-1]["end"], 10 + len(page))

    def test_page_without_lines_gets_page_block(self):
        blocks = page_blocks("文字", 5, 2, [], None)
        self.assertEqual(blocks, [{"start": 5, "end": 7, "locator": {"type": "pdf", "page": 2}}])


class WireTest(unittest.TestCase):
    def test_source_blocks_serialize_and_skip_malformed(self):
        from docreader.source_wire import source_blocks_to_proto

        doc = Document(
            content="abc",
            source_blocks=[
                {"start": 0, "end": 3, "locator": {"type": "pdf", "page": 1}},
                {"start": 2, "end": 2, "locator": {"type": "pdf", "page": 1}},
                {"start": 0, "end": 1},
            ],
        )
        wire = source_blocks_to_proto(doc)
        self.assertEqual(len(wire), 1)
        self.assertEqual(json.loads(wire[0].locator_json), {"type": "pdf", "page": 1})


class LocateColumnsTest(unittest.TestCase):
    def test_repeated_line_follows_its_own_column(self):
        text = (
            "alpha left first line\nsame tail phrase here\n"
            "bravo left third line\nsame tail phrase here\n"
            "omega right first line\nsigma right second line"
        )
        left, right = (0, 100), (120, 220)
        lines = [
            ("alpha left first line", left, 700),
            ("omega right first line", right, 700),
            ("same tail phrase here", left, 680),
            ("sigma right second line", right, 680),
            ("bravo left third line", left, 660),
            ("same tail phrase here", left, 640),
        ]
        hits = locate_lines(
            text,
            [t for t, _, _ in lines],
            [(x[0], y, x[1], y + 10) for _, x, y in lines],
        )
        self.assertEqual(hits[2], text.index("same tail"))
        self.assertEqual(hits[5], text.rindex("same tail"))

    def test_short_repeated_label_is_not_placed(self):
        text = "Figure shows the LLM pipeline\nThe LLM is prompted twice"
        hits = locate_lines(text, ["LLM", "The LLM is prompted twice"])
        self.assertIsNone(hits[0])
        self.assertEqual(hits[1], text.index("The LLM"))


class SplitLineAtGapsTest(unittest.TestCase):
    @staticmethod
    def _line(chars):
        glyphs = [
            {"ch": ch, "x0": x, "x1": x + 5, "y0": 0.0, "y1": 10.0, "i": i}
            for ch, x, i in chars
        ]
        return {"text": "".join(c for c, _, _ in chars), "bbox": (0, 0, 0, 0), "chars": glyphs}

    def test_cuts_fused_columns_at_a_stream_jump(self):
        from docreader.parser.pdf_parser import _split_line_at_gaps

        left = [(ch, 10 + 5 * k, 100 + k) for k, ch in enumerate("left side")]
        right = [(ch, 80 + 5 * k, 900 + k) for k, ch in enumerate("right side")]
        parts = _split_line_at_gaps(self._line(left + right))
        self.assertEqual([p["bbox"][0] for p in parts], [10, 80])

    def test_keeps_a_wide_word_space_within_one_column(self):
        from docreader.parser.pdf_parser import _split_line_at_gaps

        chars = [(ch, 10 + 5 * k, k) for k, ch in enumerate("wide")]
        chars += [(ch, 60 + 5 * k, 5 + k) for k, ch in enumerate("space")]
        self.assertEqual(len(_split_line_at_gaps(self._line(chars))), 1)


if __name__ == "__main__":
    unittest.main()
