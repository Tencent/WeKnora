"""Generate a styled games scoreboard workbook using openpyxl.

Usage:
  python3 create_spreadsheet_with_styling.py --output /tmp/GamesSimpleStyling.xlsx
"""

from __future__ import annotations

import argparse
from pathlib import Path

from openpyxl import Workbook
from openpyxl.formatting.rule import FormulaRule
from openpyxl.styles import Alignment, Font, PatternFill
from openpyxl.utils import get_column_letter

HEADER_FILL_HEX = "B7E1CD"
HIGHLIGHT_FILL_HEX = "FFF2CC"


def apply_header_style(cell, fill_hex: str) -> None:
    cell.fill = PatternFill("solid", fgColor=fill_hex)
    cell.font = Font(bold=True)
    cell.alignment = Alignment(horizontal="center", vertical="center")


def apply_highlight_style(cell, fill_hex: str) -> None:
    cell.fill = PatternFill("solid", fgColor=fill_hex)
    cell.font = Font(bold=True)
    cell.alignment = Alignment(horizontal="center", vertical="center")


def populate_game_sheet(ws) -> None:
    ws.title = "GameX"
    ws.row_dimensions[2].height = 24

    widths = {"B": 18, "C": 14, "D": 14, "E": 14, "F": 40}
    for col, width in widths.items():
        ws.column_dimensions[col].width = width

    headers = ["", "Name", "Game 1 Score", "Game 2 Score", "Total Score", "Notes", ""]
    for idx, value in enumerate(headers, start=1):
        cell = ws.cell(row=2, column=idx, value=value)
        if value:
            apply_header_style(cell, HEADER_FILL_HEX)

    players = [
        ("Vicky", 12, 30, "Dominated the minigames."),
        ("Yash", 20, 10, "Emily main with strong defense."),
        ("Bobby", 1000, 1030, "Numbers look suspiciously high."),
    ]
    for row_idx, (name, g1, g2, note) in enumerate(players, start=3):
        ws.cell(row=row_idx, column=2, value=name)
        ws.cell(row=row_idx, column=3, value=g1)
        ws.cell(row=row_idx, column=4, value=g2)
        ws.cell(row=row_idx, column=5, value=f"=SUM(C{row_idx}:D{row_idx})")
        ws.cell(row=row_idx, column=6, value=note)

    ws.cell(row=7, column=2, value="Winner")
    ws.cell(row=7, column=3, value="=INDEX(B3:B5, MATCH(MAX(E3:E5), E3:E5, 0))")
    ws.cell(row=7, column=5, value="Congrats!")

    ws.merge_cells("C7:D7")
    for col in range(2, 6):
        apply_highlight_style(ws.cell(row=7, column=col), HIGHLIGHT_FILL_HEX)

    rule = FormulaRule(formula=["LEN(A2)>0"], fill=PatternFill("solid", fgColor=HEADER_FILL_HEX))
    ws.conditional_formatting.add("A2:G2", rule)


def main() -> None:
    parser = argparse.ArgumentParser(description="Create a styled games scoreboard workbook.")
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("GamesSimpleStyling.xlsx"),
        help="Output .xlsx path (default: GamesSimpleStyling.xlsx)",
    )
    args = parser.parse_args()

    wb = Workbook()
    ws = wb.active
    populate_game_sheet(ws)

    for col in range(1, 8):
        col_letter = get_column_letter(col)
        if col_letter not in ws.column_dimensions:
            ws.column_dimensions[col_letter].width = 12

    args.output.parent.mkdir(parents=True, exist_ok=True)
    wb.save(args.output)
    print(f"Saved workbook to {args.output}")


if __name__ == "__main__":
    main()
