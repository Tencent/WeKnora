"""Compose the README feature spotlights: two real screenshots framed as app windows.

Writes spotlight-{name}-{light,dark}.webp into the directory above this script.
Screenshots come from website-docs/public/screenshots (the ones the product docs and homepage use)
and, for Q&A, docs/images.

    python3 docs/images/readme/src/spotlights.py
"""
import os
from PIL import Image, ImageDraw, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.dirname(HERE)
ROOT = os.path.abspath(os.path.join(OUT, "..", "..", ".."))
SHOTS = os.path.join(ROOT, "website-docs", "public", "screenshots")

# (main shot, side shot, side crop as fractions of width/height, side window width, side corner)
# The side window sits in the bottom corner where it hides the least of the main shot;
# the browser shot keeps its task preview in the bottom right, so its side window goes left.
SETS = {
    "qa": ("../../../docs/images/qa.png", "../../../docs/images/agent-qa.png", (0.30, 0.0, 0.84, 0.70), 640, "right"),
    "toolbox": ("mcp-services.png", "toolbox.png", (0.11, 0.0, 0.762, 0.32), 760, "left"),
    "browser": ("local-browser-task.png", "browser-connection.png", (0.12, 0.0, 0.62, 0.48), 700, "left"),
    "sandbox": ("skill-sandbox-chat.png", "sandbox-desktop.png", (0.545, 0.12, 1.0, 0.9), 560, "right"),
    "wiki": ("wiki-browser.png", "wiki-graph.png", (0.15, 0.05, 0.75, 0.72), 680, "right"),
    "observability": ("observability-langfuse.png", "kb-parse-timeline.png", (0.44, 0.0, 1.0, 0.5), 700, "right"),
}
THEMES = {
    "light": dict(bar=(246, 243, 237), border=(16, 31, 56, 34), dots=[(223, 209, 185)] * 3, shadow=(16, 31, 56, 70)),
    "dark": dict(bar=(22, 31, 48), border=(190, 208, 232, 46), dots=[(104, 86, 56)] * 3, shadow=(0, 0, 0, 150)),
}
W, H = 1600, 900
SCALE = 2  # layout is in 1600x900 units; the image is written at 2x so it stays sharp when opened
BAR = 30


def window(path, width, theme, crop=None):
    """Return an RGBA window: a title bar with three dots above the (optionally cropped) screenshot."""
    t = THEMES[theme]
    shot = Image.open(path).convert("RGB")
    if crop:
        x0, y0, x1, y1 = crop
        shot = shot.crop((round(x0 * shot.width), round(y0 * shot.height), round(x1 * shot.width), round(y1 * shot.height)))
    w = width * SCALE
    h = round(shot.height * w / shot.width)
    shot = shot.resize((w, h), Image.LANCZOS)
    bar, r = BAR * SCALE, 12 * SCALE
    win = Image.new("RGBA", (w, h + bar), (0, 0, 0, 0))
    mask = Image.new("L", win.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, w - 1, h + bar - 1), r, fill=255)
    body = Image.new("RGBA", win.size, t["bar"] + (255,))
    body.paste(shot, (0, bar))
    win.paste(body, (0, 0), mask)
    d = ImageDraw.Draw(win)
    for i, c in enumerate(t["dots"]):
        cx, cy, rr = (18 + i * 18) * SCALE, bar // 2, 5 * SCALE
        d.ellipse((cx - rr, cy - rr, cx + rr, cy + rr), fill=c)
    d.rounded_rectangle((0, 0, w - 1, h + bar - 1), r, outline=t["border"], width=2 * SCALE)
    return win


def drop_shadow(canvas, win, xy, theme):
    x, y = xy
    blur = 22 * SCALE
    sh = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    m = Image.new("L", win.size, 0)
    ImageDraw.Draw(m).rounded_rectangle((0, 0, win.width - 1, win.height - 1), 12 * SCALE, fill=THEMES[theme]["shadow"][3])
    sh.paste(Image.new("RGBA", win.size, THEMES[theme]["shadow"][:3] + (255,)), (x, y + 10 * SCALE), m)
    sh = sh.filter(ImageFilter.GaussianBlur(blur))
    canvas.alpha_composite(sh)
    canvas.alpha_composite(win, (x, y))


for name, (main, side, crop, side_w, corner) in SETS.items():
    for theme in THEMES:
        canvas = Image.new("RGBA", (W * SCALE, H * SCALE), (0, 0, 0, 0))
        a = window(os.path.join(SHOTS, main), 1180, theme)
        b = window(os.path.join(SHOTS, side), side_w, theme, crop)
        main_x = 24 if corner == "right" else W - 1180 - 24
        side_x = W - side_w - 24 if corner == "right" else 24
        drop_shadow(canvas, a, (main_x * SCALE, 24 * SCALE), theme)
        drop_shadow(canvas, b, (side_x * SCALE, (H - 24) * SCALE - b.height), theme)
        canvas.save(os.path.join(OUT, f"spotlight-{name}-{theme}.webp"), "WEBP", quality=84, method=6)
print("ok")
