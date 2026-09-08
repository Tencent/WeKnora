#!/usr/bin/env python3
"""Generate the fixed Topic 3 Parquet evaluation dataset from reviewed JSON."""

from __future__ import annotations

import json
import pathlib

import pyarrow as pa
import pyarrow.parquet as pq


ROOT = pathlib.Path(__file__).resolve().parent
TARGET = ROOT / "datasets" / "topic3-10"


def main() -> None:
    rows = json.loads((TARGET / "source.json").read_text(encoding="utf-8"))
    if len(rows) != 10 or [row["id"] for row in rows] != list(range(1, 11)):
        raise ValueError("topic3-10 must contain exactly the stable IDs 1..10")

    text_schema = pa.schema([("id", pa.int64()), ("text", pa.string())])
    rel_schema = pa.schema([("qid", pa.int64()), ("pid", pa.int64())])
    qa_schema = pa.schema([("qid", pa.int64()), ("aid", pa.int64())])
    pq.write_table(pa.Table.from_pylist(
        [{"id": row["id"], "text": row["question"]} for row in rows], schema=text_schema
    ), TARGET / "queries.parquet")
    pq.write_table(pa.Table.from_pylist(
        [{"id": row["id"], "text": row["passage"]} for row in rows], schema=text_schema
    ), TARGET / "corpus.parquet")
    pq.write_table(pa.Table.from_pylist(
        [{"id": row["id"], "text": row["answer"]} for row in rows], schema=text_schema
    ), TARGET / "answers.parquet")
    pq.write_table(pa.Table.from_pylist(
        [{"qid": row["id"], "pid": row["id"]} for row in rows], schema=rel_schema
    ), TARGET / "qrels.parquet")
    pq.write_table(pa.Table.from_pylist(
        [{"qid": row["id"], "aid": row["id"]} for row in rows], schema=qa_schema
    ), TARGET / "qas.parquet")


if __name__ == "__main__":
    main()
