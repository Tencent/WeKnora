#!/usr/bin/env python3
# Copyright (c) OpenAI. All rights reserved.
import argparse
import re
import sys
import tempfile
from math import ceil
from os import listdir
from os.path import basename, expanduser, isfile, join, splitext
from pathlib import Path
from typing import Literal

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from ensure_raster_image import SUPPORTED_EXTS, ensure_raster_image  # type: ignore
from PIL import Image, ImageDraw, ImageFont, ImageOps


def _make_placeholder(w: int, h: int) -> Image.Image:
    """Create a visible placeholder tile with a light gray fill and a red X cross."""
    ph = Image.new("RGBA", (w, h), (220, 220, 220, 255))
    ph_draw = ImageDraw.Draw(ph)
    line_color = (180, 0, 0, 255)
    ph_draw.line([(0, 0), (ph.width - 1, ph.height - 1)], fill=line_color, width=3)
    ph_draw.line([(ph.width - 1, 0), (0, ph.height - 1)], fill=line_color, width=3)
    return ph


def _load_images_with_placeholders(
    input_files: list[str], retain_converted_files: bool, fail_on_image_error: bool = False
) -> tuple[list[str], list[Image.Image | None]]:
    labels = [basename(p) for p in input_files]
    images: list[Image.Image | None] = []
    if retain_converted_files:
        for p in input_files:
            try:
                images.append(Image.open(ensure_raster_image(p)))
            except Exception as e:
                if fail_on_image_error:
                    raise
                print(f'Warning: Failed to convert or load image "{p}": {e}')
                images.append(None)
    else:
        with tempfile.TemporaryDirectory(prefix="montage_convert_") as tmp_conv:
            for p in input_files:
                try:
                    images.append(Image.open(ensure_raster_image(p, tmp_conv)))
                except Exception as e:
                    if fail_on_image_error:
                        raise
                    print(f'Warning: Failed to convert or load image "{p}": {e}')
                    images.append(None)
    return labels, images


def _natural_key(s: str) -> list:
    """Key function for natural sorting (e.g., Slide2 before Slide10)."""
    return [int(part) if part.isdigit() else part for part in re.split(r"(\d+)", s)]


def create_montage(
    input_files: list[str],
    output_file: str,
    num_col: int,
    cell_w: int,
    cell_h: int,
    gap: int,
    label_mode: Literal["number", "filename", "none"],
    retain_converted_files: bool = False,
    fail_on_image_error: bool = False,
) -> None:
    """Build a montage with a fixed number of columns.

    Each cell has size `cell_w` x `cell_h`. Every input image is resized isotropically to fit inside
    the cell. `gap` controls spacing around and between cells (outer margin equals gap).
    Label behavior is controlled by `label_mode` which can be one of:
      - "none": no labels are drawn
      - "number": draw a 1-based index beneath each image
      - "filename": draw the filename (no directory) beneath each image
    """

    if num_col <= 0:
        raise ValueError("num_col must be positive")
    if cell_w <= 0 or cell_h <= 0:
        raise ValueError("cell_w and cell_h must be positive")

    labels, images = _load_images_with_placeholders(
        input_files=input_files,
        retain_converted_files=retain_converted_files,
        fail_on_image_error=fail_on_image_error,
    )

    num_images = len(images)
    num_valid = sum(1 for im in images if im is not None)
    if num_valid == 0:
        raise ValueError("No valid images to render.")
    if num_valid < num_images:
        cell_size = round(min(cell_w, cell_h) * 0.6)
        placeholder = _make_placeholder(cell_size, cell_size)
    else:
        placeholder = None
    cols = num_col
    rows = ceil(num_images / cols)

    temp_canvas = Image.new("RGB", (10, 10), (255, 255, 255))
    temp_draw = ImageDraw.Draw(temp_canvas)

    # Choose a readable default font size relative to cell height
    font: ImageFont.FreeTypeFont | ImageFont.ImageFont
    try:
        # Attempt to use a common system font for clarity; fallback to default
        font_size = max(12, min(36, int(cell_h * 0.12)))
        font = ImageFont.truetype("arial.ttf", font_size)
    except Exception:
        font = ImageFont.load_default()
        # Adjust default font effect size estimate
        font_size = 12

    draw_labels = label_mode != "none"
    label_height = 0
    if draw_labels:
        # Height is approximately constant across strings for a given font
        # Use 'Ag' to approximate ascent ('A') and descender ('g') for filename text
        sample_text = "1" if label_mode == "number" else "Ag"
        lbbox = temp_draw.textbbox((0, 0), sample_text, font=font)
        label_height = ceil(lbbox[3] - lbbox[1]) + 6

    row_h = cell_h + label_height

    canvas_w = cols * cell_w + (cols + 1) * gap
    canvas_h = rows * row_h + (rows + 1) * gap
    # Light grey canvas background as in typical slide sorter view
    canvas = Image.new("RGB", (canvas_w, canvas_h), (242, 242, 242))
    draw = ImageDraw.Draw(canvas)

    for idx, img in enumerate(images):
        col = idx % cols
        row = idx // cols

        # Top-left corner of the cell including outer margin and gaps
        x0 = gap + col * (cell_w + gap)
        y0 = gap + row * (row_h + gap)

        # Fit the image within the cell while preserving aspect ratio
        if label_mode == "number":
            label = str(idx + 1)
        elif label_mode == "filename":
            label = labels[idx]
        else:
            label = ""

        if draw_labels:
            bbox = draw.textbbox((0, 0), label, font=font)
            text_w = bbox[2] - bbox[0]
        else:
            text_w = 0

        if img:
            resized = ImageOps.contain(
                img.convert("RGBA"),
                (cell_w, cell_h),
                method=Image.Resampling.LANCZOS,
            )
        else:
            print(f"Warning: Using placeholder for invalid image at row={row + 1}, col={col + 1}")
            assert placeholder is not None
            resized = placeholder

        paste_x = x0 + (cell_w - resized.width) // 2
        paste_y = y0 + (cell_h - resized.height) // 2
        canvas.paste(
            resized,
            (paste_x, paste_y),
            mask=resized.split()[3] if resized.mode == "RGBA" else None,
        )

        border_color = (160, 160, 160)
        bw = 1
        draw.rectangle(
            [
                paste_x - bw,
                paste_y - bw,
                paste_x + resized.width,
                paste_y + resized.height,
            ],
            outline=border_color,
            width=bw,
        )

        if draw_labels:
            tx = x0 + round((cell_w - text_w) / 2)
            ty = y0 + cell_h + 3
            draw.text((tx, ty), label, font=font, fill=(0, 0, 0))

    canvas.save(output_file)
    print(f"Montage saved to {output_file}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description=(
            "Create a montage with a fixed number of columns. "
            "Each image is resized isotropically to fit inside a cell of size (cell_width x cell_height)."
        )
    )
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--input_files", nargs="+", help="List of input image file paths")
    group.add_argument("--input_dir", help="Directory containing input images")
    parser.add_argument(
        "--output_file",
        required=True,
        help=(
            "Path to save the output montage image. The format is inferred from the file extension."
        ),
    )
    parser.add_argument(
        "--num_col",
        type=int,
        default=5,
        help="Number of images per row (default: 5)",
    )
    parser.add_argument(
        "--cell_width",
        type=int,
        default=400,
        help="Container width in pixels for each image (default: 400)",
    )
    parser.add_argument(
        "--cell_height",
        type=int,
        default=225,
        help="Container height in pixels for each image (default: 225)",
    )
    parser.add_argument(
        "--gap",
        type=int,
        default=16,
        help="Gap in pixels between images and canvas margins (default: 16)",
    )
    parser.add_argument(
        "--label_mode",
        choices=["number", "filename", "none"],
        default="number",
        help=(
            "Label mode: 'number' to draw 1-based indices (default), 'filename' to use the "
            "image's filename (no directory), or 'none' for no labels"
        ),
    )
    parser.add_argument(
        "--retain_converted_files",
        action="store_true",
        default=False,
        help=(
            "If set, write converted images (e.g., SVG->PNG, WDP->PNG) next to the original files "
            "instead of a temporary directory."
        ),
    )
    parser.add_argument(
        "--fail_on_image_error",
        action="store_true",
        default=False,
        help=(
            "If set, fail immediately when any image conversion/loading fails (no placeholders). "
            "By default, failures are tolerated and placeholders are used."
        ),
    )
    args = parser.parse_args()

    output_path = expanduser(args.output_file)
    if args.input_files:
        input_files = [expanduser(p) for p in args.input_files]
    else:
        input_dir = expanduser(args.input_dir)
        names = sorted(listdir(input_dir), key=_natural_key)
        dir_entries = [join(input_dir, f) for f in names]
        input_files = [
            p for p in dir_entries if isfile(p) and splitext(p)[1].lower() in SUPPORTED_EXTS
        ]
        if not input_files:
            raise ValueError(
                "No image files with supported extensions were found in the specified directory."
            )

    create_montage(
        input_files=input_files,
        output_file=output_path,
        num_col=args.num_col,
        cell_w=args.cell_width,
        cell_h=args.cell_height,
        gap=args.gap,
        label_mode=args.label_mode,
        retain_converted_files=args.retain_converted_files,
        fail_on_image_error=args.fail_on_image_error,
    )


if __name__ == "__main__":
    main()
