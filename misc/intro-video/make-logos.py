"""Derive the transparent logos in assets/ from docs/images/logo.png.

logo-light.png keeps the original colors for paper backgrounds; logo-dark.png
turns the navy lettering cream and keeps the gold wave, for navy backgrounds.
The crop matches the one the homepage uses (x=0, y=208, 945x238).
"""
from pathlib import Path

from PIL import Image

here = Path(__file__).resolve().parent
src = Image.open(here.parents[1] / "docs/images/logo.png").convert("RGB").crop((0, 208, 945, 446))
w, h = src.size
light, dark = Image.new("RGBA", (w, h)), Image.new("RGBA", (w, h))
sp, lp, dp = src.load(), light.load(), dark.load()

for y in range(h):
    for x in range(w):
        r, g, b = sp[x, y]
        a = 255 - min(r, g, b)  # coverage against the white ground
        if a < 4:
            lp[x, y] = dp[x, y] = (0, 0, 0, 0)
            continue
        f = a / 255
        rr, gg, bb = (int(max(0, min(255, (c - 255 * (1 - f)) / f))) for c in (r, g, b))
        lp[x, y] = (rr, gg, bb, a)
        dp[x, y] = (rr, gg, bb, a) if rr > bb + 40 else (246, 241, 232, a)

light.save(here / "assets/logo-light.png")
dark.save(here / "assets/logo-dark.png")
