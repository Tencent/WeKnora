#!/usr/bin/env python3
"""WeKnora PDF page extraction, rotation and text/PDF watermarks (MIT)."""
import argparse
import copy
import io
import json
from pathlib import Path

from pypdf import PdfReader, PdfWriter
from reportlab.pdfgen import canvas


def page_indices(spec, count):
    if not spec:
        return list(range(count))
    pages = []
    for chunk in spec.split(','):
        ends = chunk.strip().split('-')
        first = int(ends[0] or 1)
        last = int(ends[1] or count) if len(ends) == 2 else first
        if len(ends) > 2 or first < 1 or last > count or first > last:
            raise ValueError('page range is outside the document')
        pages.extend(range(first - 1, last))
    return pages


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('input')
    parser.add_argument('-o', '--output', required=True)
    parser.add_argument('--pages', help='1-based ranges, e.g. 1-3,7,9-')
    parser.add_argument('--rotate', type=int, choices=[0, 90, 180, 270], default=0)
    parser.add_argument('--compress', action='store_true')
    marks = parser.add_mutually_exclusive_group()
    marks.add_argument('--stamp', help='Overlay the first page of another PDF')
    marks.add_argument('--text', help='Overlay centered text')
    parser.add_argument('--under', action='store_true')
    args = parser.parse_args()
    if Path(args.input).resolve() == Path(args.output).resolve():
        parser.error('output must differ from input')
    reader, writer = PdfReader(args.input), PdfWriter()
    stamp = PdfReader(args.stamp).pages[0] if args.stamp else None
    for index in page_indices(args.pages, len(reader.pages)):
        page = copy.deepcopy(reader.pages[index])
        if args.text:
            stream = io.BytesIO()
            width, height = float(page.mediabox.width), float(page.mediabox.height)
            overlay = canvas.Canvas(stream, pagesize=(width, height))
            overlay.setFont('Helvetica', 36); overlay.setFillAlpha(0.25)
            overlay.drawCentredString(width / 2, height / 2, args.text)
            overlay.save(); stream.seek(0)
            page.merge_page(PdfReader(stream).pages[0], over=not args.under)
        elif stamp is not None:
            page.merge_page(stamp, over=not args.under)
        if args.rotate:
            page.rotate(args.rotate)
        writer.add_page(page)
        if args.compress:
            writer.pages[-1].compress_content_streams()
    with open(args.output, 'wb') as output:
        writer.write(output)
    print(json.dumps({'ok': True, 'pages': len(writer.pages), 'output': args.output}))


if __name__ == '__main__':
    main()
