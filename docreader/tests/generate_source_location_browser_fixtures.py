"""Regenerate browser fixtures with the actual builtin PDF parser.

Run from the repository root:
  docreader/.venv/bin/python -m docreader.tests.generate_source_location_browser_fixtures
"""
import base64
import json
from pathlib import Path
from unittest.mock import patch

from docreader.parser.pdf_parser import PDFParser
from docreader.tests.test_source_locator_pdf import pdf_fixture


def main():
    cases = [{"name": "PDFium lower paragraph with decimal evidence", "page": 2,
              "needle": "Second page lower evidence", "pages": [
                  {"lines": [("First page has independent original evidence.", 60, 700)]},
                  {"lines": [("Second page upper paragraph is unrelated.", 60, 700),
                             ("Second page lower evidence: the limit is 1.5 mm.", 60, 400)]}]}]
    for rotation in (0, 90, 180, 270):
        cases.append({"name": f"PDFium CropBox and rotation {rotation}", "page": 1,
                      "needle": "Cropped and rotated", "pages": [
                          {"rotation": rotation, "crop": "/CropBox [100 100 500 700]",
                           "lines": [("Cropped and rotated source evidence.", 150, 600)]}]})
    output = []
    for case in cases:
        data = pdf_fixture(case["pages"])
        with patch("docreader.parser.pdf_parser.SOURCE_BOXES", True):
            doc = PDFParser(file_name="fixture.pdf", pdf_force_scanned=False).parse_into_text(data)
        at = doc.content.index(case["needle"])
        block = next(b for b in doc.source_blocks if b["start"] <= at < b["end"])
        locator = {**block["locator"], "quote": doc.content[block["start"]:block["end"]].strip()}
        assert locator.get("bbox"), case["name"]
        output.append({"name": case["name"], "page": case["page"], "pdf": base64.b64encode(data).decode(), "locator": locator})
    target = Path(__file__).resolve().parents[2] / "frontend/tests/source-locate/parsedFixtures.json"
    target.write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    main()
