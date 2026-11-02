#!/usr/bin/env python3
"""Locale purity check.

Catches text from the wrong script sneaking into a locale file:

  zh-TW, ja-JP   must not contain Simplified Chinese
  ko-KR, ru-RU, en-US   must not contain CJK ideographs

zh-CN is intentionally unchecked: Traditional characters (place names,
proper nouns) are legitimate there.

Requires: pip install opencc

Usage:
  python3 scripts/i18n/check-locales.py [--root REPO_ROOT]

Exit code 1 when violations are found.
"""
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

try:
    import opencc
except ImportError:
    print("opencc is required: pip install opencc", file=sys.stderr)
    sys.exit(2)

# Traditional variants that must not be flagged as Simplified.
VARIANTS = set(
    "台臺裡裏著着為爲線綫麵麪峰峯群羣床牀杯盃跡蹟效傚傘繖啟啓"
    "真眞假叚眾衆教敎既旣概槪溫溫吳吴呂吕周週游遊於于餘余"
)

# Per-locale allow-list: legitimate cross-script usage.
ALLOW: dict[str, set[str]] = {
    "zh-TW.ts": {"批准", "若干", "阿里雲"},
    "ja-JP.ts": set(),
    "ko-KR.ts": set(),
    "ru-RU.ts": set(),
    "en-US.ts": set(),
}

RULES = {
    "zh-TW.ts": "no_simplified",
    "ko-KR.ts": "no_cjk",
    "ru-RU.ts": "no_cjk",
    "en-US.ts": "no_cjk",
}

# ja-JP is deliberately excluded: Japanese shinjitai share glyphs with
# Simplified Chinese (画, 声, 数, 体 ...), so a Simplified check produces
# false positives on every line.

# Language-picker entries legitimately contain endonyms in other scripts,
# e.g. ko-KR has zhCN: '简体中文' and jaJP: '日本語'.
LANG_KEY = re.compile(r"\b(zhCN|zhTW|jaJP|koKR|ruRU|enUS)\s*:")


def is_cjk(ch: str) -> bool:
    o = ord(ch)
    return 0x3400 <= o <= 0x4DBF or 0x4E00 <= o <= 0x9FFF


def main() -> int:
    ap = argparse.ArgumentParser()
    # Derive the repo root from this script's location so the check works
    # regardless of the caller's working directory (the CI step runs from
    # frontend/, where a literal "." would resolve to frontend/frontend/...).
    repo_root = Path(__file__).resolve().parent.parent.parent
    ap.add_argument(
        "--root",
        default=str(repo_root),
        help="repository root (defaults to the repo containing this script)",
    )
    args = ap.parse_args()

    locales = Path(args.root) / "frontend/src/i18n/locales"
    if not locales.is_dir():
        print(f"locales directory not found: {locales}", file=sys.stderr)
        return 2

    s2t = opencc.OpenCC("s2t")
    failed = False

    for name, rule in RULES.items():
        path = locales / name
        if not path.is_file():
            print(f"[skip] {name} not found")
            continue

        allowed = ALLOW.get(name, set())
        hits: list[tuple[int, str, str]] = []

        for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if LANG_KEY.search(line):
                continue
            if any(word in line for word in allowed):
                continue

            if rule == "no_simplified":
                bad = sorted({
                    ch for ch in line
                    if s2t.convert(ch) != ch and ch not in VARIANTS
                })
            else:  # no_cjk
                bad = sorted({ch for ch in line if is_cjk(ch)})

            if bad:
                hits.append((lineno, "".join(bad), line.strip()[:100]))

        if hits:
            failed = True
            print(f"[FAIL] {name}: {len(hits)} line(s)", file=sys.stderr)
            for lineno, chars, text in hits[:10]:
                print(f"  L{lineno} [{chars}] {text}", file=sys.stderr)
            if len(hits) > 10:
                print(f"  ... {len(hits) - 10} more", file=sys.stderr)
        else:
            print(f"[OK] {name}")

    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
