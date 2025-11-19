import logging
from io import BytesIO
from typing import List

import pandas as pd

from docreader.models.document import Chunk, Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class ExcelParser(BaseParser):
    def parse_into_text(self, content: bytes) -> Document:
        chunks: List[Chunk] = []
        text: List[str] = []
        start, end = 0, 0

        excel_file = pd.ExcelFile(BytesIO(content))
        for excel_sheet_name in excel_file.sheet_names:
            df = excel_file.parse(sheet_name=excel_sheet_name)
            df.dropna(how="all", inplace=True)

            for _, row in df.iterrows():
                page_content = []
                for k, v in row.items():
                    if pd.notna(v):
                        page_content.append(f"{k}: {v}")
                if not page_content:
                    continue
                content_row = ",".join(page_content) + "\n"
                end += len(content_row)
                text.append(content_row)
                chunks.append(
                    Chunk(content=content_row, seq=len(chunks), start=start, end=end)
                )
                start = end

        return Document(content="".join(text), chunks=chunks)


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_file = "/path/to/your/file.xlsx"
    parser = ExcelParser()
    with open(your_file, "rb") as f:
        content = f.read()
        document = parser.parse_into_text(content)
        logger.error(document.content)

        for chunk in document.chunks:
            logger.error(chunk.content)
            break
