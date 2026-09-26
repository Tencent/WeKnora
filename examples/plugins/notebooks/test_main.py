import base64
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "..", "pluginsdk", "python", "src"))
sys.path.insert(0, HERE)

from main import convert, plugin  # noqa: E402
from weknora_plugin import ErrorCode, ParseInput, PluginError  # noqa: E402
from weknora_plugin.plugin import Call  # noqa: E402

PNG = base64.b64encode(b"\x89PNG fake").decode()

NOTEBOOK = {
    "metadata": {"kernelspec": {"language": "python"}},
    "cells": [
        {"cell_type": "markdown", "source": ["# Sales report\n", "Quarterly numbers."]},
        {
            "cell_type": "code",
            "source": "print('total', 42)\nx = '```'",
            "outputs": [
                {"output_type": "stream", "name": "stdout", "text": ["total 42\n"]},
                {"output_type": "display_data", "data": {"image/png": PNG, "text/plain": "<Figure>"}},
                {"output_type": "execute_result", "data": {"text/plain": ["'" + "a" * 50 + "'"]}},
                {"output_type": "error", "ename": "ValueError", "evalue": "bad"},
            ],
        },
        {"cell_type": "code", "source": "", "outputs": []},
    ],
}


class ConvertTest(unittest.TestCase):
    def test_cells_and_outputs(self):
        out = convert(NOTEBOOK, max_output_chars=20)
        md = out.markdown
        self.assertTrue(md.startswith("# Sales report\nQuarterly numbers.\n\n````python\nprint('total', 42)"))
        self.assertIn("```text\ntotal 42\n```", md)
        self.assertIn("![Output of cell 2](images/cell1-1.png)", md)
        self.assertIn("'" + "a" * 19 + "\n…", md)
        self.assertIn("ValueError: bad", md)
        self.assertEqual([(i.original_ref, i.data, i.mime_type) for i in out.images], [("images/cell1-1.png", b"\x89PNG fake", "image/png")])
        self.assertEqual(out.metadata, {"cells": "3", "language": "python", "title": "Sales report"})

    def test_without_outputs(self):
        out = convert(NOTEBOOK, include_outputs=False)
        self.assertNotIn("total 42", out.markdown)
        self.assertEqual(out.images, [])


class ParseTest(unittest.TestCase):
    def call(self, tenant=None):
        return Call({"context": {"tenantId": 1}, "config": {"tenant": tenant or {}}})

    def test_parse(self):
        doc = ParseInput(file_name="r.ipynb", file_type="ipynb", content=json.dumps(NOTEBOOK).encode())
        parse = plugin._parsers["ipynb"]
        self.assertIn("total 42", parse(self.call(), doc).markdown)
        self.assertNotIn("total 42", parse(self.call({"include_outputs": False}), doc).markdown)

    def test_not_a_notebook(self):
        parse = plugin._parsers["ipynb"]
        for content in (b"WeKnora conformance check\n", b"[]", b"\xff\xfe"):
            with self.assertRaises(PluginError) as ctx:
                parse(self.call(), ParseInput(file_name="x.ipynb", content=content))
            self.assertEqual(ctx.exception.code, ErrorCode.BAD_REQUEST)


if __name__ == "__main__":
    unittest.main()
