#!/usr/bin/env python3
"""Functional checks for the docx skill. No network I/O."""

import argparse
import json
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = ROOT.name
COMMANDS = ['soffice', 'pdftotext']


def require(condition, detail):
    if not condition:
        raise RuntimeError(str(detail))


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=120).stdout


def check(directory):
    for command in COMMANDS:
        if not shutil.which(command):
            raise RuntimeError(f'missing runtime command: {command}')
    from docx import Document
    doc = Document()
    doc.add_heading('WeKnora smoke check', 0)
    doc.add_paragraph('Hello document 中文文档')
    from docx.oxml.ns import qn
    style = doc.styles['Normal']
    style.font.name = 'Noto Sans CJK SC'
    style.element.get_or_add_rPr().get_or_add_rFonts().set(qn('w:eastAsia'), 'Noto Sans CJK SC')
    source = directory / 'sample.docx'
    doc.save(source)
    require(Document(source).paragraphs[1].text == 'Hello document 中文文档', "Document(source).paragraphs[1].text == 'Hello document 中文文档'")
    run('soffice', '-env:UserInstallation=' + (directory / 'lo-profile').as_uri(), '--headless', '--convert-to', 'pdf', '--outdir', str(directory), str(source))
    output = directory / 'sample.pdf'
    require(output.read_bytes().startswith(b'%PDF'), "output.read_bytes().startswith(b'%PDF')")
    require('中文文档' in run('pdftotext', str(output), '-'), "'Hello document' in run('pdftotext', str(output), '-')")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', action='store_true')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='weknora-skill-smoke-') as tmp:
        check(Path(tmp))
    if args.report:
        report = ROOT / '.weknora/install-report.json'
        report.parent.mkdir(exist_ok=True)
        report.write_text(json.dumps({'commands': COMMANDS, 'blockers': []}) + '\n')
    print(json.dumps({'ok': True, 'skill': NAME, 'functional_check': 'passed'}))
if __name__ == '__main__':
    main()
