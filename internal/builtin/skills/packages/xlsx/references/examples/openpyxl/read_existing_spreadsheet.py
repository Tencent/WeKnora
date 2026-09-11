"""Read an existing .xlsx and print a small summary.

If --input is not provided, this script creates a tiny sample workbook in /tmp
and reads that instead.
"""

from __future__ import annotations

import argparse
import tempfile
from pathlib import Path

from openpyxl import Workbook, load_workbook


def create_sample(path: Path) -> Path:
    wb = Workbook()
    ws = wb.active
    ws.title = "Sample"
    ws.append(["Item", "Qty", "Price"])
    ws.append(["Apples", 3, 1.25])
    ws.append(["Oranges", 2, 0.95])
    ws.append(["Bananas", 5, 0.75])
    ws["D1"] = "Total"
    ws["D2"] = "=B2*C2"
    ws["D3"] = "=B3*C3"
    ws["D4"] = "=B4*C4"
    wb.save(path)
    return path


def main() -> None:
    parser = argparse.ArgumentParser(description="Read an existing spreadsheet.")
    parser.add_argument("--input", type=Path, help="Path to an .xlsx file")
    args = parser.parse_args()

    if args.input:
        input_path = args.input
    else:
        tmp_dir = Path(tempfile.gettempdir())
        input_path = tmp_dir / "sample_read_existing.xlsx"
        create_sample(input_path)

    wb = load_workbook(input_path, data_only=False)
    print(f"Loaded: {input_path}")
    print("Sheet names:", wb.sheetnames)

    for name in wb.sheetnames:
        ws = wb[name]
        max_row = ws.max_row or 0
        max_col = ws.max_column or 0
        print(f"\n== {name} (rows: {max_row}, cols: {max_col})")
        for row in ws.iter_rows(min_row=1, max_row=min(max_row, 5), max_col=min(max_col, 5)):
            values = [cell.value for cell in row]
            print(values)


if __name__ == "__main__":
    main()
