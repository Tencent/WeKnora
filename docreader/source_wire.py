"""Wire form of source blocks (see docreader.parser.source_locator).

Kept apart from docreader.main so it can be imported without the parser
registry.
"""

import json

from docreader.proto.docreader_pb2 import SourceBlock


def source_blocks_to_proto(document) -> list:
    """Convert a document's source blocks to their wire form.

    Malformed entries are skipped: positions are an optional extra and must
    never fail a parse.
    """
    out = []
    for block in getattr(document, "source_blocks", None) or []:
        try:
            start, end = int(block["start"]), int(block["end"])
            locator = block["locator"]
            if end <= start or start < 0 or not locator.get("type"):
                continue
            out.append(
                SourceBlock(
                    start=start,
                    end=end,
                    locator_json=json.dumps(locator, ensure_ascii=False),
                )
            )
        except (KeyError, TypeError, ValueError, AttributeError):
            continue
    return out
