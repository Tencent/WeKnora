"""
Excel Parser Module

This module provides functionality to parse Excel files (.xlsx, .xls) into
structured Document objects with text content and chunks. It supports multiple
sheets and handles various Excel formats using pandas.
"""
import logging
import posixpath
import re
import xml.etree.ElementTree as ET
from io import BytesIO
from typing import List
import zipfile

import pandas as pd

from docreader.models.document import Chunk, Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)

MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"
OFFICE_REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
XML_NS = {"m": MAIN_NS, "r": REL_NS}
CELL_REF_RE = re.compile(r"([A-Z]+)")


class ExcelParser(BaseParser):
    """Parser for Excel files (.xlsx, .xls).
    
    This parser extracts text content from Excel files by processing all sheets
    and converting each row into a structured text format. Each row becomes a
    separate chunk with key-value pairs.
    
    Features:
        - Supports multiple sheets in a single Excel file
        - Automatically removes completely empty rows
        - Converts each row to "column: value" format
        - Creates individual chunks for each row for better granularity
        
    Example:
        >>> parser = ExcelParser()
        >>> with open("data.xlsx", "rb") as f:
        ...     content = f.read()
        ...     document = parser.parse_into_text(content)
        >>> print(document.content)
        Name: John,Age: 30,City: NYC
        Name: Jane,Age: 25,City: LA
    """
    
    def parse_into_text(self, content: bytes) -> Document:
        """Parse Excel file bytes into a Document object.
        
        Args:
            content: Raw bytes of the Excel file
            
        Returns:
            Document: Parsed document containing:
                - content: Full text with all rows from all sheets
                - chunks: List of Chunk objects, one per row
                
        Note:
            - Empty rows (all NaN values) are automatically skipped
            - Each row is formatted as: "col1: val1,col2: val2,..."
            - Chunks maintain sequential ordering across all sheets
        """
        # Load Excel file from bytes into pandas ExcelFile object
        try:
            excel_file = pd.ExcelFile(BytesIO(content))
        except Exception as exc:
            try:
                return self._parse_xlsx_without_styles(content)
            except Exception:
                raise exc
        
        rows: List[List[str]] = []

        # Process each sheet in the Excel file
        for excel_sheet_name in excel_file.sheet_names:
            # Parse the sheet into a DataFrame
            df = excel_file.parse(sheet_name=excel_sheet_name)
            # Remove rows where all values are NaN (completely empty rows)
            df.dropna(how="all", inplace=True)

            # Process each row in the DataFrame
            for _, row in df.iterrows():
                page_content = []
                # Build key-value pairs for non-null values
                for k, v in row.items():
                    if pd.notna(v):  # Skip NaN/null values
                        page_content.append(f"{k}: {v}")
                
                # Skip rows with no valid content
                if not page_content:
                    continue

                rows.append(page_content)

        # Combine all text and return as Document
        return self._build_document(rows)

    def _parse_xlsx_without_styles(self, content: bytes) -> Document:
        rows: List[List[str]] = []
        with zipfile.ZipFile(BytesIO(content)) as zf:
            shared_strings = self._load_shared_strings(zf)
            for _, sheet_path in self._load_sheet_paths(zf):
                sheet_rows = self._load_sheet_rows(zf, sheet_path, shared_strings)
                if not sheet_rows:
                    continue
                headers = sheet_rows[0]
                for values in sheet_rows[1:]:
                    page_content = []
                    for idx, value in enumerate(values):
                        if not value:
                            continue
                        header = headers[idx] if idx < len(headers) and headers[idx] else f"Column{idx + 1}"
                        page_content.append(f"{header}: {value}")
                    if page_content:
                        rows.append(page_content)
        return self._build_document(rows)

    @staticmethod
    def _build_document(rows: List[List[str]]) -> Document:
        chunks: List[Chunk] = []
        text: List[str] = []
        start, end = 0, 0
        for page_content in rows:
            content_row = ",".join(page_content) + "\n"
            end += len(content_row)
            text.append(content_row)
            chunks.append(Chunk(content=content_row, seq=len(chunks), start=start, end=end))
            start = end
        return Document(content="".join(text), chunks=chunks)

    @staticmethod
    def _load_shared_strings(zf: zipfile.ZipFile) -> List[str]:
        try:
            root = ET.fromstring(zf.read("xl/sharedStrings.xml"))
        except KeyError:
            return []
        strings = []
        for item in root.findall("m:si", XML_NS):
            parts = [text.text or "" for text in item.findall(".//m:t", XML_NS)]
            strings.append("".join(parts))
        return strings

    @staticmethod
    def _load_sheet_paths(zf: zipfile.ZipFile) -> List[tuple[str, str]]:
        workbook = ET.fromstring(zf.read("xl/workbook.xml"))
        rels = ET.fromstring(zf.read("xl/_rels/workbook.xml.rels"))
        targets = {}
        for rel in rels.findall("r:Relationship", XML_NS):
            rel_id = rel.get("Id")
            target = rel.get("Target", "")
            if target.startswith("/"):
                sheet_path = target.lstrip("/")
            else:
                sheet_path = posixpath.normpath(posixpath.join("xl", target))
            targets[rel_id] = sheet_path

        sheets = []
        for sheet in workbook.findall(".//m:sheet", XML_NS):
            rel_id = sheet.get(f"{{{OFFICE_REL_NS}}}id")
            sheet_path = targets.get(rel_id)
            if sheet_path:
                sheets.append((sheet.get("name", ""), sheet_path))
        return sheets

    @staticmethod
    def _load_sheet_rows(zf: zipfile.ZipFile, sheet_path: str, shared_strings: List[str]) -> List[List[str]]:
        root = ET.fromstring(zf.read(sheet_path))
        rows = []
        for row in root.findall(".//m:sheetData/m:row", XML_NS):
            cells = {}
            for cell in row.findall("m:c", XML_NS):
                value = ExcelParser._cell_value(cell, shared_strings)
                if value == "":
                    continue
                cells[ExcelParser._cell_column_index(cell)] = value
            if cells:
                rows.append([cells.get(idx, "") for idx in range(max(cells) + 1)])
        return rows

    @staticmethod
    def _cell_column_index(cell: ET.Element) -> int:
        ref = cell.get("r", "")
        match = CELL_REF_RE.match(ref)
        if not match:
            return 0
        index = 0
        for char in match.group(1):
            index = index * 26 + ord(char) - ord("A") + 1
        return index - 1

    @staticmethod
    def _cell_value(cell: ET.Element, shared_strings: List[str]) -> str:
        cell_type = cell.get("t")
        if cell_type == "inlineStr":
            return "".join(text.text or "" for text in cell.findall(".//m:t", XML_NS)).strip()

        value_node = cell.find("m:v", XML_NS)
        if value_node is None or value_node.text is None:
            return ""

        value = value_node.text.strip()
        if cell_type == "s":
            try:
                return shared_strings[int(value)].strip()
            except (ValueError, IndexError):
                return ""
        if cell_type == "b":
            return "TRUE" if value == "1" else "FALSE"
        return value


if __name__ == "__main__":
    # Example usage: Parse an Excel file and display results
    logging.basicConfig(level=logging.DEBUG)

    # Specify the path to your Excel file
    your_file = "/path/to/your/file.xlsx"
    parser = ExcelParser()
    
    # Read and parse the Excel file
    with open(your_file, "rb") as f:
        content = f.read()
        document = parser.parse_into_text(content)
        
        # Display the full document content
        logger.error(document.content)

        # Display the first chunk as an example
        for chunk in document.chunks:
            logger.error(chunk.content)
            break  # Only show the first chunk
