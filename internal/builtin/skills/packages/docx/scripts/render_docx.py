import argparse
import os
import re
import subprocess
import tempfile
import xml.etree.ElementTree as ET
from os import makedirs, replace
from os.path import abspath, basename, exists, expanduser, join, splitext
from shutil import which
import sys
from typing import Sequence, cast
from zipfile import ZipFile

from pdf2image import convert_from_path, pdfinfo_from_path

TWIPS_PER_INCH: int = 1440


def ensure_system_tools() -> None:
    missing: list[str] = []
    for tool in ("soffice", "pdftoppm"):
        if which(tool) is None:
            missing.append(tool)
    if missing:
        tools = ", ".join(missing)
        raise RuntimeError(
            f"Missing required system tool(s): {tools}. Install LibreOffice and Poppler, then retry."
        )


def calc_dpi_via_ooxml_docx(input_path: str, max_w_px: int, max_h_px: int) -> int:
    """Calculate DPI from OOXML `word/document.xml` page size (w:pgSz in twips).

    DOCX stores page dimensions in section properties as twips (1/1440 inch).
    We read the first encountered section's page size and compute an isotropic DPI
    that fits within the target max pixel dimensions.
    """
    with ZipFile(input_path, "r") as zf:
        xml = zf.read("word/document.xml")
    root = ET.fromstring(xml)
    ns = {"w": "http://schemas.openxmlformats.org/wordprocessingml/2006/main"}

    # Common placements: w:body/w:sectPr or w:body/w:p/w:pPr/w:sectPr
    sect_pr = root.find(".//w:sectPr", ns)
    if sect_pr is None:
        raise RuntimeError("Section properties not found in document.xml")
    pg_sz = sect_pr.find("w:pgSz", ns)
    if pg_sz is None:
        raise RuntimeError("Page size not found in section properties")

    # Values are in twips
    w_twips_str = pg_sz.get(
        "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}w"
    ) or pg_sz.get("w")
    h_twips_str = pg_sz.get(
        "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}h"
    ) or pg_sz.get("h")

    if not w_twips_str or not h_twips_str:
        raise RuntimeError("Page size attributes missing in pgSz")

    width_in = int(w_twips_str) / TWIPS_PER_INCH
    height_in = int(h_twips_str) / TWIPS_PER_INCH
    if width_in <= 0 or height_in <= 0:
        raise RuntimeError("Invalid page size values in document.xml")
    return round(min(max_w_px / width_in, max_h_px / height_in))


def calc_dpi_via_pdf(input_path: str, max_w_px: int, max_h_px: int) -> int:
    """Convert input to PDF and compute DPI from its page size."""
    with tempfile.TemporaryDirectory(prefix="soffice_profile_") as user_profile:
        with tempfile.TemporaryDirectory(prefix="soffice_convert_") as convert_tmp_dir:
            stem = splitext(basename(input_path))[0]
            pdf_path = convert_to_pdf(input_path, user_profile, convert_tmp_dir, stem)
            if not (pdf_path and exists(pdf_path)):
                raise RuntimeError("Failed to convert input to PDF for DPI computation.")

            info = pdfinfo_from_path(pdf_path)
            size_val = info.get("Page size")
            if not size_val:
                for k, v in info.items():
                    if isinstance(v, str) and "size" in k.lower() and "pts" in v:
                        size_val = v
                        break
            if not isinstance(size_val, str):
                raise RuntimeError("Failed to read PDF page size for DPI computation.")

            m = re.search(r"(\d+)\s*x\s*(\d+)\s*pts", size_val)
            if not m:
                raise RuntimeError("Unrecognized PDF page size format.")
            width_pts = int(m.group(1))
            height_pts = int(m.group(2))
            width_in = width_pts / 72.0
            height_in = height_pts / 72.0
            if width_in <= 0 or height_in <= 0:
                raise RuntimeError("Invalid PDF page size values.")
            return round(min(max_w_px / width_in, max_h_px / height_in))


def run_cmd_no_check(cmd: list[str]) -> None:
    subprocess.run(
        cmd,
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env=os.environ.copy(),
    )


def convert_to_pdf(
    doc_path: str,
    user_profile: str,
    convert_tmp_dir: str,
    stem: str,
) -> str:
    # Try direct DOC(X) -> PDF
    cmd_pdf = [
        "soffice",
        "-env:UserInstallation=file://" + user_profile,
        "--invisible",
        "--headless",
        "--norestore",
        "--convert-to",
        "pdf",
        "--outdir",
        convert_tmp_dir,
        doc_path,
    ]
    run_cmd_no_check(cmd_pdf)

    pdf_path = join(convert_tmp_dir, f"{stem}.pdf")
    if exists(pdf_path):
        return pdf_path

    # Fallback: DOCX -> ODT, then ODT -> PDF
    cmd_odt = [
        "soffice",
        "-env:UserInstallation=file://" + user_profile,
        "--invisible",
        "--headless",
        "--norestore",
        "--convert-to",
        "odt",
        "--outdir",
        convert_tmp_dir,
        doc_path,
    ]
    run_cmd_no_check(cmd_odt)

    odt_path = join(convert_tmp_dir, f"{stem}.odt")

    if exists(odt_path):
        cmd_odt_pdf = [
            "soffice",
            "-env:UserInstallation=file://" + user_profile,
            "--invisible",
            "--headless",
            "--norestore",
            "--convert-to",
            "pdf",
            "--outdir",
            convert_tmp_dir,
            odt_path,
        ]
        run_cmd_no_check(cmd_odt_pdf)
        if exists(pdf_path):
            return pdf_path

    return ""


def rasterize(
    doc_path: str,
    out_dir: str,
    dpi: int,
) -> Sequence[str]:
    """Rasterise DOCX (or similar) to images placed in out_dir and return their paths.

    Images are named as page-<N>.<ext> with pages starting at 1.
    """
    makedirs(out_dir, exist_ok=True)
    doc_path = abspath(doc_path)
    stem = splitext(basename(doc_path))[0]

    # Use a unique user profile to avoid LibreOffice profile lock when running concurrently
    with tempfile.TemporaryDirectory(prefix="soffice_profile_") as user_profile:
        # Write conversion outputs into a temp directory to avoid any IO oddities
        with tempfile.TemporaryDirectory(prefix="soffice_convert_") as convert_tmp_dir:
            pdf_path = convert_to_pdf(
                doc_path,
                user_profile,
                convert_tmp_dir,
                stem,
            )

            if not pdf_path or not exists(pdf_path):
                raise RuntimeError(
                    "Failed to produce PDF for rasterization (direct and ODT fallback)."
                )
            paths_raw = cast(
                list[str],
                convert_from_path(
                    pdf_path,
                    dpi=dpi,
                    fmt="png",
                    thread_count=8,
                    output_folder=out_dir,
                    paths_only=True,
                    output_file="page",
                ),
            )

    # Rename convert_from_path's output format f'page{thread_id:04d}-{page_num:02d}.<ext>' to 'page-<num>.<ext>'
    pages: list[tuple[int, str]] = []
    for src_path in paths_raw:
        base = splitext(basename(src_path))[0]
        page_num_str = base.split("-")[-1]
        page_num = int(page_num_str)
        dst_path = join(out_dir, f"page-{page_num}.png")
        replace(src_path, dst_path)
        pages.append((page_num, dst_path))
    pages.sort(key=lambda t: t[0])
    final_paths = [path for _, path in pages]
    return final_paths


def main() -> None:
    parser = argparse.ArgumentParser(description="Render DOCX-like file to PNG images.")
    parser.add_argument(
        "input_path",
        type=str,
        help="Path to the input DOCX file (or compatible).",
    )
    parser.add_argument(
        "--output_dir",
        type=str,
        default=None,
        help=(
            "Output directory for the rendered images. "
            "Defaults to a folder next to the input named after the input file (without extension)."
        ),
    )
    parser.add_argument(
        "--width",
        type=int,
        default=1600,
        help=(
            "Approximate maximum width in pixels after isotropic scaling (default 1600). "
            "The actual value may exceed slightly."
        ),
    )
    parser.add_argument(
        "--height",
        type=int,
        default=2000,
        help=(
            "Approximate maximum height in pixels after isotropic scaling (default 2000). "
            "The actual value may exceed slightly."
        ),
    )
    parser.add_argument(
        "--dpi",
        type=int,
        default=None,
        help=("Override computed DPI. If provided, skips DOCX/PDF-based DPI calculation."),
    )
    args = parser.parse_args()

    try:
        ensure_system_tools()

        input_path = abspath(expanduser(args.input_path))
        out_dir = (
            abspath(expanduser(args.output_dir)) if args.output_dir else splitext(input_path)[0]
        )

        if args.dpi is not None:
            dpi = int(args.dpi)
        else:
            try:
                if input_path.lower().endswith((".docx", ".docm", ".dotx", ".dotm")):
                    dpi = calc_dpi_via_ooxml_docx(input_path, args.width, args.height)
                else:
                    raise RuntimeError("Skip OOXML DPI; not a DOCX container")
            except Exception:
                dpi = calc_dpi_via_pdf(input_path, args.width, args.height)

        rasterize(input_path, out_dir, dpi)
        print("Pages rendered to " + out_dir)
    except RuntimeError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
