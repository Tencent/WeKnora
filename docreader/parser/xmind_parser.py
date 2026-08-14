"""Parse XMind archives into Markdown outlines."""

import io
import json
import zipfile
from dataclasses import dataclass, field

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser


MAX_CONTENT_BYTES = 32 * 1024 * 1024


@dataclass
class _Topic:
    title: str = ""
    note: str = ""
    children: list["_Topic"] = field(default_factory=list)


@dataclass
class _Sheet:
    title: str = ""
    root_topic: _Topic | None = None


def _clean_text(value: object) -> str:
    return value.strip() if isinstance(value, str) else ""


def _topic_from_json(value: object) -> _Topic | None:
    if not isinstance(value, dict):
        return None

    note = ""
    notes = value.get("notes")
    if isinstance(notes, dict):
        plain = notes.get("plain")
        if isinstance(plain, dict):
            note = _clean_text(plain.get("content"))

    topics: list[_Topic] = []
    children = value.get("children")
    if isinstance(children, dict):
        attached = children.get("attached")
        if isinstance(attached, list):
            for child in attached:
                topic = _topic_from_json(child)
                if topic is not None:
                    topics.append(topic)

    return _Topic(
        title=_clean_text(value.get("title")),
        note=note,
        children=topics,
    )


def _parse_json_sheets(payload: bytes) -> list[_Sheet]:
    values = json.loads(payload)
    if not isinstance(values, list):
        raise ValueError("invalid XMind content.json: expected a sheet list")

    sheets: list[_Sheet] = []
    for value in values:
        if not isinstance(value, dict):
            continue
        sheets.append(
            _Sheet(
                title=_clean_text(value.get("title")),
                root_topic=_topic_from_json(value.get("rootTopic")),
            )
        )
    return sheets


def _render_topic(topic: _Topic, depth: int) -> tuple[list[str], int, int]:
    lines: list[str] = []
    topic_count = 0
    note_count = 0
    child_depth = depth

    if topic.title:
        lines.append(f"{'  ' * depth}- {topic.title}")
        topic_count = 1
        child_depth += 1
        if topic.note:
            for note_line in topic.note.splitlines():
                normalized = note_line.strip()
                quote = f"> {normalized}" if normalized else ">"
                lines.append(f"{'  ' * child_depth}{quote}")
            note_count = 1

    for child in topic.children:
        child_lines, child_topics, child_notes = _render_topic(child, child_depth)
        lines.extend(child_lines)
        topic_count += child_topics
        note_count += child_notes

    return lines, topic_count, note_count


def _render_sheets(sheets: list[_Sheet]) -> tuple[str, int, int, int]:
    rendered_sheets: list[str] = []
    topic_count = 0
    note_count = 0

    for index, sheet in enumerate(sheets, start=1):
        if sheet.root_topic is None:
            continue
        lines, sheet_topics, sheet_notes = _render_topic(sheet.root_topic, 0)
        if not lines:
            continue
        title = sheet.title or f"Sheet {index}"
        rendered_sheets.append(f"# {title}\n\n" + "\n".join(lines))
        topic_count += sheet_topics
        note_count += sheet_notes

    return (
        "\n\n---\n\n".join(rendered_sheets),
        len(rendered_sheets),
        topic_count,
        note_count,
    )


class XMindParser(BaseParser):
    """Extract topic hierarchy and plain-text notes from XMind files."""

    def parse_into_text(self, content: bytes) -> Document:
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            payload = archive.read("content.json")

        markdown, sheet_count, topic_count, note_count = _render_sheets(
            _parse_json_sheets(payload)
        )
        return Document(
            content=markdown,
            metadata={
                "source_format": "xmind",
                "xmind_content_format": "json",
                "file_size": len(content),
                "sheet_count": sheet_count,
                "topic_count": topic_count,
                "note_count": note_count,
            },
        )
