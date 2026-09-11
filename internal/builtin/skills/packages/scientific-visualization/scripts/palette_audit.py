#!/usr/bin/env python3
"""Audit palette contrast and heuristic grayscale distinguishability.

WCAG contrast calculations are exact for the supplied sRGB values. Whether a
specific WCAG success criterion applies depends on how a color is used. The
grayscale delta-L* threshold is a screening heuristic, not a standard.
"""

from __future__ import annotations

import argparse
import importlib.util
import itertools
import math
import sys
from pathlib import Path
from types import ModuleType
from typing import Any, Iterable

from _common import CliError, emit_json, positive_float

SCHEMA_VERSION = "1.0"
DEFAULT_BACKGROUND = "#FFFFFF"
DEFAULT_GRAYSCALE_DELTA = 10.0
ROLE_THRESHOLDS = {
    "graphical": 3.0,
    "large-text": 3.0,
    "normal-text": 4.5,
    "enhanced-text": 7.0,
}


def parse_hex_color(value: str) -> tuple[int, int, int]:
    """Parse an opaque six-digit sRGB color."""
    normalized = value.strip().lstrip("#")
    if len(normalized) != 6:
        raise CliError(f"expected six-digit hex color, got {value!r}")
    try:
        channels = tuple(int(normalized[index : index + 2], 16) for index in (0, 2, 4))
    except ValueError as exc:
        raise CliError(f"invalid hex color: {value!r}") from exc
    return channels  # type: ignore[return-value]


def canonical_hex(rgb: tuple[int, int, int]) -> str:
    return "#" + "".join(f"{channel:02X}" for channel in rgb)


def _linear_channel(channel: int) -> float:
    encoded = channel / 255.0
    return encoded / 12.92 if encoded <= 0.04045 else ((encoded + 0.055) / 1.055) ** 2.4


def relative_luminance(rgb: tuple[int, int, int]) -> float:
    """Return WCAG relative luminance for an sRGB color."""
    red, green, blue = (_linear_channel(channel) for channel in rgb)
    return 0.2126 * red + 0.7152 * green + 0.0722 * blue


def contrast_ratio(
    first: tuple[int, int, int], second: tuple[int, int, int]
) -> float:
    """Return WCAG contrast ratio for two opaque sRGB colors."""
    first_luminance = relative_luminance(first)
    second_luminance = relative_luminance(second)
    lighter = max(first_luminance, second_luminance)
    darker = min(first_luminance, second_luminance)
    return (lighter + 0.05) / (darker + 0.05)


def cie_lstar(rgb: tuple[int, int, int]) -> float:
    """Return CIE L* from sRGB/D65, used for grayscale screening."""
    luminance = relative_luminance(rgb)
    delta = 6.0 / 29.0
    if luminance > delta**3:
        transformed = luminance ** (1.0 / 3.0)
    else:
        transformed = luminance / (3.0 * delta**2) + 4.0 / 29.0
    return 116.0 * transformed - 16.0


def _load_palette_asset() -> ModuleType:
    asset = Path(__file__).resolve().parents[1] / "assets" / "color_palettes.py"
    spec = importlib.util.spec_from_file_location(
        "_scientific_visualization_color_palettes", asset
    )
    if spec is None or spec.loader is None:
        raise CliError(f"cannot load bundled palette asset: {asset}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def bundled_palettes() -> dict[str, list[str]]:
    """Load bundled palettes without importing Matplotlib."""
    module = _load_palette_asset()
    palettes = getattr(module, "PALETTES", None)
    if not isinstance(palettes, dict):
        raise CliError("bundled color_palettes.py does not define PALETTES")
    normalized: dict[str, list[str]] = {}
    for name, colors in palettes.items():
        if isinstance(name, str) and isinstance(colors, (list, tuple)):
            normalized[name] = [canonical_hex(parse_hex_color(item)) for item in colors]
    return normalized


def audit_palette(
    colors: Iterable[str],
    *,
    background: str = DEFAULT_BACKGROUND,
    role: str = "graphical",
    contrast_threshold: float | None = None,
    grayscale_min_delta: float = DEFAULT_GRAYSCALE_DELTA,
    name: str | None = None,
) -> dict[str, Any]:
    """Return deterministic palette contrast and grayscale screening results."""
    if role not in ROLE_THRESHOLDS and contrast_threshold is None:
        raise CliError(
            f"unknown role {role!r}; provide an explicit contrast threshold"
        )
    threshold = (
        float(contrast_threshold)
        if contrast_threshold is not None
        else ROLE_THRESHOLDS[role]
    )
    if not math.isfinite(threshold) or threshold <= 1:
        raise CliError("contrast threshold must be finite and greater than 1")
    if not math.isfinite(grayscale_min_delta) or grayscale_min_delta <= 0:
        raise CliError("grayscale delta threshold must be positive and finite")

    canonical = [canonical_hex(parse_hex_color(color)) for color in colors]
    if not canonical:
        raise CliError("at least one color is required")
    if len(canonical) > 32:
        raise CliError("palette is limited to 32 colors")
    background_rgb = parse_hex_color(background)
    background_hex = canonical_hex(background_rgb)

    color_records: list[dict[str, Any]] = []
    parsed: list[tuple[int, int, int]] = []
    for index, color in enumerate(canonical):
        rgb = parse_hex_color(color)
        parsed.append(rgb)
        ratio = contrast_ratio(rgb, background_rgb)
        color_records.append(
            {
                "index": index,
                "hex": color,
                "rgb": list(rgb),
                "relative_luminance": round(relative_luminance(rgb), 6),
                "cie_lstar": round(cie_lstar(rgb), 3),
                "contrast_against_background": round(ratio, 3),
                "background_screen": "pass" if ratio >= threshold else "review",
            }
        )

    pair_records: list[dict[str, Any]] = []
    for first, second in itertools.combinations(range(len(parsed)), 2):
        first_rgb = parsed[first]
        second_rgb = parsed[second]
        delta = abs(cie_lstar(first_rgb) - cie_lstar(second_rgb))
        pair_records.append(
            {
                "first_index": first,
                "second_index": second,
                "first_hex": canonical[first],
                "second_hex": canonical[second],
                "contrast_ratio": round(contrast_ratio(first_rgb, second_rgb), 3),
                "grayscale_delta_lstar": round(delta, 3),
                "grayscale_screen": (
                    "pass" if delta >= grayscale_min_delta else "review"
                ),
            }
        )

    background_reviews = sum(
        item["background_screen"] == "review" for item in color_records
    )
    grayscale_reviews = sum(
        item["grayscale_screen"] == "review" for item in pair_records
    )
    return {
        "schema_version": SCHEMA_VERSION,
        "palette": {
            "name": name,
            "colors": color_records,
            "color_count": len(color_records),
        },
        "background": background_hex,
        "contrast_screen": {
            "role": role,
            "threshold": threshold,
            "review_count": background_reviews,
            "basis": (
                "WCAG 2.2 contrast mathematics. Applicability depends on use; "
                "for example, SC 1.4.11 covers graphical objects required to "
                "understand web content."
            ),
        },
        "grayscale_screen": {
            "minimum_delta_lstar": grayscale_min_delta,
            "review_count": grayscale_reviews,
            "pair_count": len(pair_records),
            "basis": (
                "Heuristic CIE L* separation after removing hue. It does not "
                "predict all viewing, printing, or color-vision conditions."
            ),
        },
        "pairs": pair_records,
        "notice": (
            "No palette can establish accessibility by itself. Preserve labels "
            "and add redundant encodings such as markers, line styles, direct "
            "labels, or patterns; evaluate the rendered figure in context."
        ),
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Audit sRGB palette contrast against a background and screen "
            "pairwise grayscale distinguishability. No network is used."
        )
    )
    source = parser.add_mutually_exclusive_group(required=False)
    source.add_argument(
        "--palette", help="bundled palette name (use --list-palettes)"
    )
    source.add_argument(
        "--color",
        action="append",
        default=[],
        help="six-digit hex color; repeat for each palette color",
    )
    parser.add_argument(
        "--list-palettes",
        action="store_true",
        help="print bundled palette names and exit",
    )
    parser.add_argument("--background", default=DEFAULT_BACKGROUND)
    parser.add_argument(
        "--role",
        choices=sorted(ROLE_THRESHOLDS),
        default="graphical",
        help="select the WCAG contrast screening threshold",
    )
    parser.add_argument(
        "--contrast-threshold",
        type=positive_float,
        help="override the role threshold",
    )
    parser.add_argument(
        "--grayscale-min-delta",
        type=positive_float,
        default=DEFAULT_GRAYSCALE_DELTA,
        help=(
            "heuristic minimum CIE L* separation "
            f"(default: {DEFAULT_GRAYSCALE_DELTA})"
        ),
    )
    parser.add_argument(
        "--fail-on",
        choices=("none", "background", "grayscale", "any"),
        default="none",
        help="return exit 1 for selected review findings (default: none)",
    )
    parser.add_argument("--output", help="optional JSON report path")
    parser.add_argument(
        "--force", action="store_true", help="overwrite an existing JSON report"
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    try:
        args = parser.parse_args(argv)
        palettes = bundled_palettes()
        if args.list_palettes:
            emit_json(
                {
                    "schema_version": SCHEMA_VERSION,
                    "palettes": {
                        name: {"count": len(colors), "colors": colors}
                        for name, colors in sorted(palettes.items())
                    },
                },
                output=args.output,
                force=args.force,
            )
            return 0
        if args.palette:
            if args.palette not in palettes:
                raise CliError(
                    f"unknown palette {args.palette!r}; "
                    f"available: {', '.join(sorted(palettes))}"
                )
            colors = palettes[args.palette]
            palette_name = args.palette
        else:
            colors = args.color
            palette_name = "custom"
        if not colors:
            raise CliError("provide --palette or at least one --color")

        report = audit_palette(
            colors,
            background=args.background,
            role=args.role,
            contrast_threshold=args.contrast_threshold,
            grayscale_min_delta=args.grayscale_min_delta,
            name=palette_name,
        )
        emit_json(report, output=args.output, force=args.force)
        background_review = report["contrast_screen"]["review_count"] > 0
        grayscale_review = report["grayscale_screen"]["review_count"] > 0
        should_fail = (
            (args.fail_on in {"background", "any"} and background_review)
            or (args.fail_on in {"grayscale", "any"} and grayscale_review)
        )
        return 1 if should_fail else 0
    except CliError as exc:
        parser.exit(2, f"error: {exc}\n")


if __name__ == "__main__":
    sys.exit(main())
