"""Register the image's embeddable TrueType CJK font for ReportLab."""
from pathlib import Path
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont


def register_cjk_font():
    path = Path('/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc')
    if not path.is_file():
        raise RuntimeError('Missing embeddable CJK font: install fonts-wqy-zenhei')
    name = 'WeKnoraCJK'
    if name not in pdfmetrics.getRegisteredFontNames():
        pdfmetrics.registerFont(TTFont(name, str(path), subfontIndex=0))
    return name
