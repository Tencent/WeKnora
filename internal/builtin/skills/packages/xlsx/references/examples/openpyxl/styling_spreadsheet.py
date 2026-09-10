"""Create a styled spreadsheet with headers, borders, and a total row.

Usage:
  python3 styling_spreadsheet.py --output /tmp/styling_spreadsheet.xlsx
"""

from __future__ import annotations

import argparse
from pathlib import Path

from openpyxl import Workbook
from openpyxl.styles import Alignment, Border, Font, PatternFill, Side


def main() -> None:
    parser = argparse.ArgumentParser(description="Create a styled spreadsheet example.")
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("styling_spreadsheet.xlsx"),
        help="Output .xlsx path (default: styling_spreadsheet.xlsx)",
    )
    args = parser.parse_args()

    wb = Workbook()
    ws = wb.active
    ws.title = "FirstGame"

    ws.merge_cells("B2:E2")
    ws["B2"] = "Name | Game 1 Score | Game 2 Score | Total Score"

    header_fill = PatternFill("solid", fgColor="B7E1CD")
    header_font = Font(bold=True)
    header_alignment = Alignment(horizontal="center", vertical="center")
    ws["B2"].fill = header_fill
    ws["B2"].font = header_font
    ws["B2"].alignment = header_alignment

    ws["B3"] = "Vicky"
    ws["C3"] = 50
    ws["D3"] = 60
    ws["E3"] = "=C3+D3"

    ws["B4"] = "John"
    ws["C4"] = 40
    ws["D4"] = 50
    ws["E4"] = "=C4+D4"

    ws["B5"] = "Jane"
    ws["C5"] = 30
    ws["D5"] = 40
    ws["E5"] = "=C5+D5"

    ws["B6"] = "Jim"
    ws["C6"] = 20
    ws["D6"] = 30
    ws["E6"] = "=C6+D6"

    ws.merge_cells("B9:E9")
    ws["B9"] = "=SUM(E3:E6)"

    thin = Side(style="thin")
    border = Border(top=thin, bottom=thin, left=thin, right=thin)
    ws["B9"].border = border
    ws["B9"].alignment = Alignment(horizontal="center")
    ws["B9"].font = Font(bold=True)

    for col in ("B", "C", "D", "E"):
        ws.column_dimensions[col].width = 18
    ws.row_dimensions[2].height = 24

    args.output.parent.mkdir(parents=True, exist_ok=True)
    wb.save(args.output)
    print(f"Saved workbook to {args.output}")


if __name__ == "__main__":
    main()
