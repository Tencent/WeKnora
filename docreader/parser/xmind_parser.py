"""Parse XMind archives into Markdown outlines."""

import io
import json
import zipfile
from dataclasses import dataclass, field
from xml.etree import ElementTree
from xml.etree.ElementTree import Element

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


def _local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1].split(":", 1)[-1]


def _direct_children(element: Element, name: str) -> list[Element]:
    return [child for child in element if _local_name(child.tag) == name]


def _first_child(element: Element, name: str) -> Element | None:
    children = _direct_children(element, name)
    return children[0] if children else None


def _element_text(element: Element | None) -> str:
    if element is None:
        return ""
    return "".join(element.itertext()).strip()


def _topic_from_xml(element: Element) -> _Topic:
    notes = _first_child(element, "notes")
    plain = _first_child(notes, "plain") if notes is not None else None

    topics: list[_Topic] = []
    for children in _direct_children(element, "children"):
        for topic_group in _direct_children(children, "topics"):
            topics.extend(
                _topic_from_xml(topic)
                for topic in _direct_children(topic_group, "topic")
            )

    return _Topic(
        title=_element_text(_first_child(element, "title")),
        note=_element_text(plain),
        children=topics,
    )


def _parse_xml_sheets(payload: bytes) -> list[_Sheet]:
    try:
        root = ElementTree.fromstring(payload)
    except ElementTree.ParseError as exc:
        raise ValueError("invalid XMind content.xml") from exc

    sheets: list[_Sheet] = []
    for sheet in _direct_children(root, "sheet"):
        root_topic = _first_child(sheet, "topic")
        sheets.append(
            _Sheet(
                title=_element_text(_first_child(sheet, "title")),
                root_topic=(
                    _topic_from_xml(root_topic) if root_topic is not None else None
                ),
            )
        )
    return sheets


def _read_content_entry(content: bytes) -> tuple[str, bytes]:
    try:
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            try:
                info = archive.getinfo("content.json")
                content_format = "json"
            except KeyError:
                try:
                    info = archive.getinfo("content.xml")
                except KeyError as exc:
                    raise ValueError(
                        "XMind archive is missing content.json or content.xml"
                    ) from exc
                content_format = "xml"

            if info.flag_bits & 0x1:
                raise ValueError("encrypted XMind content is not supported")
            if info.file_size > MAX_CONTENT_BYTES:
                raise ValueError("XMind content entry exceeds the 32 MiB limit")

            with archive.open(info) as entry:
                payload = entry.read(MAX_CONTENT_BYTES + 1)
            if len(payload) > MAX_CONTENT_BYTES:
                raise ValueError("XMind content entry exceeds the 32 MiB limit")
            return content_format, payload
    except zipfile.BadZipFile as exc:
        raise ValueError("invalid XMind archive") from exc


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
        content_format, payload = _read_content_entry(content)

        if content_format == "json":
            try:
                sheets = _parse_json_sheets(payload)
            except (json.JSONDecodeError, UnicodeDecodeError) as exc:
                raise ValueError("invalid XMind content.json") from exc
        else:
            sheets = _parse_xml_sheets(payload)

        markdown, sheet_count, topic_count, note_count = _render_sheets(
            sheets
        )
        if not markdown:
            raise ValueError("XMind archive contains no renderable topics")
        return Document(
            content=markdown,
            metadata={
                "source_format": "xmind",
                "xmind_content_format": content_format,
                "file_size": len(content),
                "sheet_count": sheet_count,
                "topic_count": topic_count,
                "note_count": note_count,
            },
        )
