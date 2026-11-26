import unittest

from docreader.parser.parser import Parser


class CSVCellBoundaryTests(unittest.TestCase):
    def test_pipe_and_multiline_cells_stay_in_one_column_and_row(self):
        data = b'"Name|code",Notes\r\nPump,"left|right"\r\nValve,"line one\r\nline two"\r\n'
        for engine in ["", "builtin", "markitdown"]:
            with self.subTest(engine=engine):
                result = Parser().parse_file("records.csv", "csv", data, engine)
                self.assertEqual(
                    result.content,
                    "| Name\\|code | Notes |\n| --- | --- |\n"
                    "| Pump | left\\|right |\n| Valve | line one<br />line two |",
                )

    def test_existing_row_width_policy_is_preserved(self):
        result = Parser().parse_file("ragged.csv", "csv", b"A,B\nx,y,z\nshort\n")
        self.assertEqual(
            result.content, "| A | B |\n| --- | --- |\n| x | y |\n| short |  |"
        )

    def test_backslashes_before_pipes_cannot_reopen_a_cell_boundary(self):
        result = Parser().parse_file(
            "slashes.csv", "csv", b'A,B\nx,"C:\\folder\\|value"\n'
        )
        self.assertIn("C:\\\\folder\\\\\\|value", result.content)

    def test_encoding_and_plain_inputs_keep_their_values(self):
        for encoding in ["utf-8", "utf-16"]:
            with self.subTest(encoding=encoding):
                raw = "名称,值\n编号,00123\n状态,N/A\n".encode(encoding)
                result = Parser().parse_file("encoded.csv", "csv", raw)
                self.assertIn("| 名称 | 值 |", result.content)
                self.assertIn("| 编号 | 00123 |", result.content)
                self.assertIn("| 状态 | N/A |", result.content)
        self.assertEqual(Parser().parse_file("empty.csv", "csv", b"").content, "")
        self.assertEqual(
            Parser().parse_file("plain.csv", "csv", b"A,B\nx,2\n").content,
            "| A | B |\n| --- | --- |\n| x | 2 |",
        )
