"""Multiline XMind titles should stay in the owning outline node."""
import io
import json
import unittest
import zipfile

from docreader.parser.xmind_parser import XMindParser


def archive(name: str, data: str) -> bytes:
    result = io.BytesIO()
    with zipfile.ZipFile(result, 'w') as output:
        output.writestr(name, data)
    return result.getvalue()


class MultilineTitlesTest(unittest.TestCase):
    def test_json_and_xml_titles_preserve_tree_structure(self):
        modern = json.dumps([{'id': 'sheet', 'title': 'Quarter\n# Plan', 'rootTopic': {
            'id': 'root', 'title': 'Release', 'children': {'attached': [
                {'id': 'task', 'title': 'Prepare release\r\n- do not deploy',
                 'children': {'attached': [{'id': 'child', 'title': 'Approval'}]}}
            ]}}}])
        classic = '''<xmap-content xmlns="urn:xmind:xmap:xmlns:content:2.0">
          <sheet id="sheet"><title>Quarter\n# Plan</title><topic id="root"><title>Release</title>
          <children><topics type="attached"><topic id="task"><title>Prepare release\n- do not deploy</title>
          <children><topics type="attached"><topic id="child"><title>Approval</title></topic>
          </topics></children></topic></topics></children></topic></sheet></xmap-content>'''
        expected = '# Quarter # Plan\n\n- Release\n  - Prepare release - do not deploy\n    - Approval'
        for payload in (archive('content.json', modern), archive('content.xml', classic)):
            with self.subTest(format=payload[:10]):
                document = XMindParser(file_name='plan.xmind').parse_into_text(payload)
                self.assertEqual(document.content, expected)
                self.assertEqual(document.metadata['topic_count'], 3)

    def test_unicode_separators_blank_lines_and_existing_inline_markdown(self):
        modern = json.dumps([{'id': 's', 'title': '中文\u2028\n  计划', 'rootTopic': {
            'id': 'r', 'title': '**准备**\u2028\n  - 检查',
            'notes': {'plain': {'content': '备注\n第二行'}},
            'children': {'attached': [{'id': 'c', 'title': '`通过`'}]},
        }}], ensure_ascii=False)
        document = XMindParser(file_name='plan.xmind').parse_into_text(archive('content.json', modern))
        self.assertEqual(document.content, '# 中文 计划\n\n- **准备** - 检查\n  > 备注\n  > 第二行\n  - `通过`')
        self.assertEqual(document.metadata['note_count'], 1)
        self.assertEqual(document.metadata['topic_count'], 2)


if __name__ == '__main__':
    unittest.main()
