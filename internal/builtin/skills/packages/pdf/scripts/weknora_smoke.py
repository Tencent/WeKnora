#!/usr/bin/env python3
"""Functional checks for the pdf skill. No network I/O."""

import argparse
import json
import shutil
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAME = ROOT.name
COMMANDS = ['pdftoppm']


def require(condition, detail):
    if not condition:
        raise RuntimeError(str(detail))


def check(directory):
    for command in COMMANDS:
        if not shutil.which(command):
            raise RuntimeError(f'missing runtime command: {command}')
    from reportlab.pdfgen import canvas
    from pypdf import PdfReader, PdfWriter
    import pdfplumber
    import pypdfium2
    source = directory / 'sample.pdf'
    c = canvas.Canvas(str(source))
    from weknora_fonts import register_cjk_font
    c.setFont(register_cjk_font(), 16)
    c.drawString(80, 700, 'Hello PDF 中文字体')
    c.save()
    require('中文字体' in PdfReader(source).pages[0].extract_text(), "'Hello PDF' in PdfReader(source).pages[0].extract_text()")
    with pdfplumber.open(source) as document:
        require('中文字体' in document.pages[0].extract_text(), "'Hello PDF' in document.pages[0].extract_text()")
    document = pypdfium2.PdfDocument(str(source))
    page = document[0]
    bitmap = page.render(scale=0.25)
    require(bitmap.width > 0, 'bitmap.width > 0')
    bitmap.close()
    page.close()
    document.close()
    writer = PdfWriter()
    writer.append(source)
    output = directory / 'copy.pdf'
    writer.write(output)
    require(len(PdfReader(output).pages) == 1, 'len(PdfReader(output).pages) == 1')


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
