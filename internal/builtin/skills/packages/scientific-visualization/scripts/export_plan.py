#!/usr/bin/env python3
"""Build and screen against dated publication-export planning snapshots."""

from __future__ import annotations

import argparse
import json
import math
import sys
from pathlib import Path
from typing import Any

from _common import CliError, emit_json, positive_float

SCHEMA_VERSION = "1.0"
PROFILE_PATH = Path(__file__).resolve().parents[1] / "assets" / "publisher_profiles.json"


def load_profiles() -> dict[str, Any]:
    try:
        document = json.loads(PROFILE_PATH.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise CliError(f"cannot load publisher profile asset: {exc}") from exc
    if not isinstance(document.get("profiles"), dict):
        raise CliError("publisher profile asset does not contain profiles")
    return document


def build_plan(
    publisher: str,
    *,
    figure_type: str,
    width: str | None = None,
    phase: str | None = None,
) -> dict[str, Any]:
    """Build an explicit plan without exporting or claiming compliance."""
    document = load_profiles()
    try:
        profile = document["profiles"][publisher]
    except KeyError as exc:
        raise CliError(
            f"unknown publisher profile {publisher!r}; "
            f"available: {', '.join(sorted(document['profiles']))}"
        ) from exc
    widths = profile.get("widths_mm") or {}
    if width is not None and width not in widths:
        raise CliError(
            f"unknown width {width!r} for {publisher}; "
            f"available: {', '.join(sorted(widths))}"
        )
    format_map = profile.get("formats") or {}
    dpi_map = profile.get("raster_dpi") or {}
    available_types = sorted(set(format_map) | set(dpi_map))
    if figure_type not in available_types and available_types:
        raise CliError(
            f"unknown figure type {figure_type!r} for {publisher}; "
            f"available: {', '.join(available_types)}"
        )

    profile_phase = profile.get("phase")
    phase_matches = phase is None or phase == profile_phase
    selected_width = float(widths[width]) if width is not None else None
    return {
        "schema_version": SCHEMA_VERSION,
        "publisher": publisher,
        "label": profile["label"],
        "scope": profile["scope"],
        "profile_accessed": document["accessed"],
        "profile_phase": profile_phase,
        "requested_phase": phase,
        "phase_matches_snapshot": phase_matches,
        "journal_specific": profile.get("journal_specific"),
        "figure_type": figure_type,
        "width": {
            "name": width,
            "millimeters": selected_width,
            "available": widths,
        },
        "max_height_mm": profile.get("max_height_mm"),
        "formats": format_map.get(figure_type) if format_map else None,
        "raster_dpi": dpi_map.get(figure_type) if dpi_map else None,
        "color_modes": profile.get("color_modes"),
        "preferred_color_mode": profile.get("preferred_color_mode"),
        "max_file_bytes": profile.get("max_file_bytes"),
        "max_file_bytes_exclusive": profile.get("max_file_bytes_exclusive"),
        "width_range_px_at_300_dpi": profile.get("width_range_px_at_300_dpi"),
        "max_height_px_at_300_dpi": profile.get("max_height_px_at_300_dpi"),
        "sources": profile["sources"],
        "notes": profile.get("notes", []),
        "notice": document["notice"],
    }


def _finding(
    name: str,
    status: str,
    *,
    actual: Any,
    expected: Any,
    detail: str,
) -> dict[str, Any]:
    return {
        "name": name,
        "status": status,
        "actual": actual,
        "expected": expected,
        "detail": detail,
    }


def _normalized_mode(mode: str | None) -> str | None:
    if mode is None:
        return None
    if mode in {"RGB", "RGBA"}:
        return "RGB"
    if mode in {"L", "LA", "1", "I", "I;16", "F"}:
        return "L"
    return mode


def _effective_dimensions(
    metadata: dict[str, Any], target_width_mm: float | None
) -> tuple[float | None, float | None, float | None]:
    """Return effective DPI, width mm, and height mm."""
    width_px = metadata.get("width_px")
    height_px = metadata.get("height_px")
    if target_width_mm is not None and width_px:
        dpi = float(width_px) / (target_width_mm / 25.4)
        height_mm = (
            float(height_px) / float(width_px) * target_width_mm
            if height_px
            else None
        )
        return dpi, target_width_mm, height_mm
    width_mm = metadata.get("width_mm") or metadata.get("width_mm_from_metadata")
    height_mm = metadata.get("height_mm") or metadata.get("height_mm_from_metadata")
    dpi_values = [
        float(value)
        for value in (metadata.get("dpi_x"), metadata.get("dpi_y"))
        if value
    ]
    return (
        min(dpi_values) if dpi_values else None,
        float(width_mm) if width_mm is not None else None,
        float(height_mm) if height_mm is not None else None,
    )


def validate_against_plan(
    plan: dict[str, Any],
    input_path: str | Path,
    *,
    width_tolerance_mm: float = 1.0,
) -> dict[str, Any]:
    """Screen inspectable file properties against one explicit plan."""
    try:
        from image_metadata import inspect_file
    except ImportError as exc:
        raise CliError(f"cannot import image_metadata.py: {exc}") from exc
    inspected = inspect_file(input_path)
    metadata = inspected["metadata"]
    findings: list[dict[str, Any]] = []

    formats = plan.get("formats")
    actual_format = str(metadata.get("format", "")).lower()
    if formats is None:
        findings.append(
            _finding(
                "format",
                "unknown",
                actual=actual_format,
                expected=None,
                detail="The profile does not state a universal format for this type.",
            )
        )
    elif not formats:
        findings.append(
            _finding(
                "format",
                "unknown",
                actual=actual_format,
                expected=[],
                detail="No raster format was stated on the cited profile page.",
            )
        )
    else:
        findings.append(
            _finding(
                "format",
                "pass" if actual_format in formats else "fail",
                actual=actual_format,
                expected=formats,
                detail="Compared with formats explicitly recorded in the snapshot.",
            )
        )

    target_width = plan["width"]["millimeters"]
    effective_dpi, effective_width, effective_height = _effective_dimensions(
        metadata, target_width
    )
    dpi_rule = plan.get("raster_dpi")
    if metadata.get("kind") == "raster" and dpi_rule:
        if effective_dpi is None:
            dpi_status = "unknown"
        elif (
            dpi_rule.get("min") is not None
            and effective_dpi < float(dpi_rule["min"])
            and not math.isclose(
                effective_dpi,
                float(dpi_rule["min"]),
                rel_tol=1e-12,
                abs_tol=1e-9,
            )
        ):
            dpi_status = "fail"
        elif (
            dpi_rule.get("min_exclusive") is not None
            and effective_dpi <= float(dpi_rule["min_exclusive"])
        ):
            dpi_status = "fail"
        elif (
            dpi_rule.get("max") is not None
            and effective_dpi > float(dpi_rule["max"])
            and not math.isclose(
                effective_dpi,
                float(dpi_rule["max"]),
                rel_tol=1e-12,
                abs_tol=1e-9,
            )
        ):
            dpi_status = "fail"
        else:
            dpi_status = "pass"
        findings.append(
            _finding(
                "effective_raster_dpi",
                dpi_status,
                actual=effective_dpi,
                expected=dpi_rule,
                detail=(
                    "Calculated at the selected final width when supplied; "
                    "otherwise uses embedded DPI metadata. Upsampling is not "
                    "evidence of added detail."
                ),
            )
        )
    elif dpi_rule and metadata.get("kind") != "raster":
        findings.append(
            _finding(
                "effective_raster_dpi",
                "unknown",
                actual=None,
                expected=dpi_rule,
                detail=(
                    "Vector-container DPI is not meaningful and embedded raster "
                    "objects were not individually inspected."
                ),
            )
        )
    else:
        findings.append(
            _finding(
                "effective_raster_dpi",
                "unknown",
                actual=effective_dpi,
                expected=dpi_rule,
                detail="No binding DPI rule was recorded for this figure type.",
            )
        )

    if target_width is not None:
        if effective_width is None:
            width_status = "unknown"
        else:
            width_status = (
                "pass"
                if abs(effective_width - target_width) <= width_tolerance_mm
                else "fail"
            )
        findings.append(
            _finding(
                "final_width_mm",
                width_status,
                actual=effective_width,
                expected={
                    "target": target_width,
                    "tolerance": width_tolerance_mm,
                },
                detail=(
                    "Raster inputs are evaluated at the requested target width; "
                    "vector containers use their page dimensions."
                ),
            )
        )

    max_height = plan.get("max_height_mm")
    if max_height is not None:
        findings.append(
            _finding(
                "final_height_mm",
                (
                    "unknown"
                    if effective_height is None
                    else (
                        "pass"
                        if effective_height <= float(max_height)
                        else "fail"
                    )
                ),
                actual=effective_height,
                expected={"max": max_height},
                detail="Height includes the exported page/canvas, not a caption.",
            )
        )

    pixel_range = plan.get("width_range_px_at_300_dpi")
    if pixel_range and metadata.get("width_px") is not None:
        actual_width_px = int(metadata["width_px"])
        findings.append(
            _finding(
                "pixel_width_snapshot",
                (
                    "pass"
                    if int(pixel_range[0]) <= actual_width_px <= int(pixel_range[1])
                    else "fail"
                ),
                actual=actual_width_px,
                expected={"min": pixel_range[0], "max": pixel_range[1]},
                detail="This publisher expresses its width range at 300 dpi.",
            )
        )

    max_bytes = plan.get("max_file_bytes")
    max_bytes_exclusive = plan.get("max_file_bytes_exclusive")
    if max_bytes is not None or max_bytes_exclusive is not None:
        actual_bytes = inspected["input"]["size_bytes"]
        if max_bytes_exclusive is not None:
            size_status = (
                "pass" if actual_bytes < int(max_bytes_exclusive) else "fail"
            )
            expected_size = {"less_than": max_bytes_exclusive}
        else:
            size_status = "pass" if actual_bytes <= int(max_bytes) else "fail"
            expected_size = {"max": max_bytes}
        findings.append(
            _finding(
                "file_size",
                size_status,
                actual=actual_bytes,
                expected=expected_size,
                detail="Compared with the dated snapshot's file-size limit.",
            )
        )

    allowed_modes = plan.get("color_modes")
    actual_mode = metadata.get("mode")
    if allowed_modes:
        normalized_mode = _normalized_mode(actual_mode)
        findings.append(
            _finding(
                "color_mode",
                (
                    "unknown"
                    if normalized_mode is None
                    else (
                        "pass"
                        if normalized_mode in allowed_modes
                        else "fail"
                    )
                ),
                actual=actual_mode,
                expected=allowed_modes,
                detail=(
                    "Raster mode is screened coarsely. ICC profiles and print "
                    "conversion require separate color-management review."
                ),
            )
        )

    if metadata.get("has_alpha") is True:
        findings.append(
            _finding(
                "transparency",
                "review",
                actual=True,
                expected="verify against target journal/background",
                detail=(
                    "The raster has alpha/transparency. Blending can change "
                    "contrast, and no universal publisher rule is assumed."
                ),
            )
        )

    counts = {
        status: sum(item["status"] == status for item in findings)
        for status in ("pass", "fail", "review", "unknown")
    }
    return {
        "schema_version": SCHEMA_VERSION,
        "plan": plan,
        "inspection": inspected,
        "findings": findings,
        "screening_summary": counts,
        "notice": (
            "This screen compares only machine-readable properties with a dated "
            "profile. It cannot certify journal acceptance, font appearance, "
            "embedded-raster quality, accessibility, or scientific integrity."
        ),
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Plan publication export from dated official-source snapshots and "
            "optionally screen a local file. No network access is used."
        )
    )
    parser.add_argument("--list", action="store_true", help="list profiles")
    parser.add_argument("--publisher", help="publisher/profile key")
    parser.add_argument(
        "--figure-type",
        default="combination",
        help="figure type in the selected profile (default: combination)",
    )
    parser.add_argument("--width", help="named final width from the profile")
    parser.add_argument(
        "--phase", help="your submission phase, for an explicit scope check"
    )
    parser.add_argument("--input", help="optional local file to screen")
    parser.add_argument(
        "--width-tolerance-mm",
        type=positive_float,
        default=1.0,
        help="vector/page width tolerance (default: 1.0)",
    )
    parser.add_argument("--output", help="optional JSON output path")
    parser.add_argument("--force", action="store_true")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    try:
        args = parser.parse_args(argv)
        document = load_profiles()
        if args.list:
            emit_json(
                {
                    "schema_version": SCHEMA_VERSION,
                    "accessed": document["accessed"],
                    "profiles": {
                        name: {
                            "label": profile["label"],
                            "scope": profile["scope"],
                            "phase": profile["phase"],
                            "sources": profile["sources"],
                        }
                        for name, profile in sorted(document["profiles"].items())
                    },
                    "notice": document["notice"],
                },
                output=args.output,
                force=args.force,
            )
            return 0
        if not args.publisher:
            parser.error("--publisher is required unless --list is used")
        plan = build_plan(
            args.publisher,
            figure_type=args.figure_type,
            width=args.width,
            phase=args.phase,
        )
        result = (
            validate_against_plan(
                plan,
                args.input,
                width_tolerance_mm=args.width_tolerance_mm,
            )
            if args.input
            else plan
        )
        emit_json(result, output=args.output, force=args.force)
        if args.input:
            return 1 if result["screening_summary"]["fail"] else 0
        return 0
    except CliError as exc:
        parser.exit(2, f"error: {exc}\n")


if __name__ == "__main__":
    sys.exit(main())
