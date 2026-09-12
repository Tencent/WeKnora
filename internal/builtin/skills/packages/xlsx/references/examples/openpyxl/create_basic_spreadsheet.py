"""Create a basic spreadsheet with two sheets and a simple formula.

Usage:
  python3 create_basic_spreadsheet.py --output /tmp/basic_spreadsheet.xlsx
"""

from __future__ import annotations

import argparse
from pathlib import Path

from openpyxl import Workbook
from openpyxl.utils import get_column_letter


def main() -> None:
    parser = argparse.ArgumentParser(description="Create a basic spreadsheet with example data.")
    parser.add_argument(
        "--output",
        type=Path,
        default=Path("basic_spreadsheet.xlsx"),
        help="Output .xlsx path (default: basic_spreadsheet.xlsx)",
    )
    args = parser.parse_args()

    wb = Workbook()
    overview = wb.active
    overview.title = "Overview"
    employees = wb.create_sheet("Employees")

    overview["A1"] = "Description"
    overview["A2"] = "Awesome Company Report"

    employees.append(["Title", "Name", "Address", "Score"])
    employees.append(["Engineer", "Vicky", "90 50th Street", 98])
    employees.append(["Manager", "Alex", "500 Market Street", 92])
    employees.append(["Designer", "Jordan", "200 Pine Street", 88])

    employees["A6"] = "Total Score"
    employees["D6"] = "=SUM(D2:D4)"

    for col in range(1, 5):
        employees.column_dimensions[get_column_letter(col)].width = 20

    args.output.parent.mkdir(parents=True, exist_ok=True)
    wb.save(args.output)
    print(f"Saved workbook to {args.output}")


if __name__ == "__main__":
    main()
