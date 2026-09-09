#!/usr/bin/env python3
"""Emit the closed capability matrix or a redacted local-file manifest."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any

from _capabilities import (
    REFERENCE_ONLY_FORMATS,
    automated_capability_rows,
    capability_for_path,
    validate_magic,
)
from _common import (
    DEFAULT_MAX_FILE_BYTES,
    CliError,
    bounded_file_limit,
    checked_input_file,
    display_identifier,
    emit_json,
    run_cli,
    sha256_file,
    stable_token,
)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "List exact EDA capabilities or inspect a bounded local file without "
            "content sniffing, raw previews, or path disclosure."
        )
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    list_parser = subparsers.add_parser(
        "list",
        help="Print automated and reference-only capability rows",
    )
    list_parser.add_argument("--output", help="Optional local .json output path")
    list_parser.add_argument(
        "--root",
        default=".",
        help="Existing local directory that bounds the optional output",
    )
    list_parser.add_argument("--force", action="store_true")

    inspect_parser = subparsers.add_parser(
        "inspect",
        help="Create a redacted manifest for one local regular file",
    )
    inspect_parser.add_argument("input", help="Local file path inside --root")
    inspect_parser.add_argument(
        "--root",
        default=".",
        help="Existing local directory that bounds all input/output paths",
    )
    inspect_parser.add_argument(
        "--max-bytes",
        type=int,
        default=DEFAULT_MAX_FILE_BYTES,
        help=f"Maximum input bytes (hard ceiling: {512 * 1024 * 1024})",
    )
    inspect_parser.add_argument(
        "--sha256",
        action="store_true",
        help="Explicitly include a full-file SHA-256 content fingerprint",
    )
    inspect_parser.add_argument(
        "--reveal-identifiers",
        action="store_true",
        help="Include only a sanitized basename; never include the full path",
    )
    inspect_parser.add_argument("--output", help="Optional local .json output path")
    inspect_parser.add_argument("--force", action="store_true")
    return parser


def capability_matrix() -> dict[str, Any]:
    """Return exact script and reference-only registrations."""

    return {
        "schema_version": "1.1",
        "policy": {
            "unknown_formats": "rejected",
            "content_sniffing_fallback": False,
            "reference_only_means_executable_support": False,
            "all_automated_inspection_is_bounded": True,
        },
        "automated": automated_capability_rows(),
        "reference_only": [
            {"suffix": suffix, **REFERENCE_ONLY_FORMATS[suffix]}
            for suffix in sorted(REFERENCE_ONLY_FORMATS)
        ],
    }


def inspect_manifest(
    path: Path,
    *,
    max_bytes: int,
    include_sha256: bool = False,
    reveal_identifiers: bool = False,
) -> dict[str, Any]:
    """Build a redacted manifest from a previously checked local file."""

    capability = capability_for_path(path)
    signature_check_supported = capability["suffix"] not in {
        ".csv",
        ".tsv",
        ".json",
    }
    if capability["tier"].startswith("automated") and signature_check_supported:
        validate_magic(path, capability["suffix"])
    info = path.stat()
    manifest: dict[str, Any] = {
        "schema_version": "1.1",
        "file": {
            "file_id": stable_token(str(path), kind="file"),
            "basename": display_identifier(
                path.name,
                kind="filename",
                reveal_identifiers=reveal_identifiers,
            ),
            "full_path_emitted": False,
            "size_bytes": info.st_size,
            "regular_file": True,
            "symlink": False,
        },
        "capability": capability,
        "inspection": {
            "format_signature_checked": (
                capability["tier"].startswith("automated")
                and signature_check_supported
            ),
            "content_values_read": False,
            "reference_only": capability["tier"] == "reference_only",
        },
    }
    if include_sha256:
        manifest["file"]["sha256"] = sha256_file(path, max_bytes=max_bytes)
        manifest["file"]["sha256_explicitly_requested"] = True
    return manifest


def _main() -> None:
    args = build_parser().parse_args()
    if args.command == "list":
        emit_json(
            capability_matrix(),
            output=args.output,
            root=args.root,
            force=args.force,
        )
        return
    if args.command != "inspect":
        raise CliError("unsupported command")
    max_bytes = bounded_file_limit(args.max_bytes)
    path = checked_input_file(
        args.input,
        root=args.root,
        max_bytes=max_bytes,
    )
    report = inspect_manifest(
        path,
        max_bytes=max_bytes,
        include_sha256=args.sha256,
        reveal_identifiers=args.reveal_identifiers,
    )
    emit_json(
        report,
        output=args.output,
        root=args.root,
        force=args.force,
    )


def main() -> int:
    return run_cli(_main)


if __name__ == "__main__":
    raise SystemExit(main())
