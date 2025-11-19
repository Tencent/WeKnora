import logging
from io import BytesIO
from typing import List

import pandas as pd

from docreader.models.document import Chunk, Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)


class CSVParser(BaseParser):
    def parse_into_text(self, content: bytes) -> Document:
        chunks: List[Chunk] = []
        text: List[str] = []
        start, end = 0, 0

        df = pd.read_csv(BytesIO(content), on_bad_lines="skip")

        for i, (idx, row) in enumerate(df.iterrows()):
            content_row = (
                ",".join(
                    f"{col.strip()}: {str(row[col]).strip()}" for col in df.columns
                )
                + "\n"
            )
            end += len(content_row)
            text.append(content_row)
            chunks.append(Chunk(content=content_row, seq=i, start=start, end=end))
            start = end

        return Document(
            content="".join(text),
            chunks=chunks,
        )


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_file = "/path/to/your/file.csv"
    parser = CSVParser()
    with open(your_file, "rb") as f:
        content = f.read()
        document = parser.parse_into_text(content)
        logger.error(document.content)

        for chunk in document.chunks:
            logger.error(chunk.content)
