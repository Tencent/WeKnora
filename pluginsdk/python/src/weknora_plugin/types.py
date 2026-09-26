"""The protocol's messages as dataclasses, and their JSON form: camelCase
keys, bytes as base64, times as RFC 3339. Optional fields left at their
default are omitted, like Go's omitempty; unknown fields are ignored."""

from __future__ import annotations

import base64
import dataclasses
import re
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional, Type, TypeVar, Union

T = TypeVar("T")


def _camel(name: str) -> str:
    head, *rest = name.split("_")
    return head + "".join(p[:1].upper() + p[1:] for p in rest)


_FRACTION = re.compile(r"(\.\d{6})\d+")


def parse_time(value: Any) -> Optional[datetime]:
    """Reads an RFC 3339 time as Go writes it (nanoseconds, a Z suffix)."""
    if not value:
        return None
    if isinstance(value, datetime):
        return value
    s = _FRACTION.sub(r"\1", str(value).replace("Z", "+00:00"))
    return datetime.fromisoformat(s)


def format_time(value: datetime) -> str:
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def to_wire(value: Any) -> Any:
    """Converts a message (or anything JSON-like holding messages) to its
    JSON form."""
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        out = {}
        for f in dataclasses.fields(value):
            v = getattr(value, f.name)
            optional = f.default is not dataclasses.MISSING or f.default_factory is not dataclasses.MISSING
            if optional and (v is None or v is False or v == "" or v == b"" or v == [] or v == {}):
                continue
            out[_camel(f.name)] = to_wire(v)
        return out
    if isinstance(value, (bytes, bytearray)):
        return base64.b64encode(value).decode()
    if isinstance(value, datetime):
        return format_time(value)
    if isinstance(value, dict):
        return {k: to_wire(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [to_wire(v) for v in value]
    return value


def from_wire(cls: Type[T], data: Any) -> T:
    """Builds a message from its JSON form."""
    if isinstance(data, cls):
        return data
    data = data or {}
    kwargs = {}
    hints = _hints(cls)
    for f in dataclasses.fields(cls):
        key = _camel(f.name)
        if key not in data:
            continue
        kwargs[f.name] = _convert(hints[f.name], data[key])
    return cls(**kwargs)


def _hints(cls: type) -> Dict[str, Any]:
    import typing

    return typing.get_type_hints(cls)


def _convert(hint: Any, value: Any) -> Any:
    import typing

    if value is None:
        return None
    origin = typing.get_origin(hint)
    args = typing.get_args(hint)
    if origin is Union:
        inner = [a for a in args if a is not type(None)]
        if len(inner) == 1:
            return _convert(inner[0], value)
        if bytes in inner and isinstance(value, str):
            return base64.b64decode(value)
        return value
    if hint is bytes:
        return base64.b64decode(value) if isinstance(value, str) else bytes(value)
    if hint is datetime:
        return parse_time(value)
    if dataclasses.is_dataclass(hint):
        return from_wire(hint, value)
    if origin in (list, List) and args and isinstance(value, list):
        return [_convert(args[0], v) for v in value]
    return value


@dataclass
class ConnectorConfig:
    """A data source's instance configuration. Credentials are decrypted;
    do not keep them beyond the call."""

    credentials: Dict[str, Any] = field(default_factory=dict)
    settings: Dict[str, Any] = field(default_factory=dict)
    resource_ids: List[str] = field(default_factory=list)


@dataclass
class Resource:
    """Something a data source can sync: a space, a folder, a feed."""

    external_id: str
    name: str
    type: str = ""
    description: str = ""
    url: str = ""
    modified_at: Optional[datetime] = None
    parent_id: str = ""
    has_children: bool = False
    metadata: Optional[Dict[str, Any]] = None


@dataclass
class Cursor:
    """A connector's resumable state; WeKnora stores it and hands it back.
    A checkpoint must be a complete snapshot of progress so far."""

    last_sync_time: Optional[datetime] = None
    state: Optional[Dict[str, Any]] = None


FETCH_FULL = "full"
FETCH_INCREMENTAL = "incremental"


@dataclass
class FetchInput:
    """Starts or resumes a sync; cursor None starts from scratch."""

    mode: str = FETCH_FULL
    cursor: Optional[Cursor] = None
    resource_ids: List[str] = field(default_factory=list)


@dataclass
class FetchedItem:
    """One document fetched from the source. Content is the body, Markdown
    preferred; a str is sent as UTF-8."""

    external_id: str
    title: str
    content: Optional[Union[bytes, str]] = None
    content_type: str = ""
    file_name: str = ""
    url: str = ""
    updated_at: Optional[datetime] = None
    created_at: Optional[datetime] = None
    metadata: Optional[Dict[str, str]] = None
    is_deleted: bool = False
    source_resource_id: str = ""

    def __post_init__(self) -> None:
        if isinstance(self.content, str):
            self.content = self.content.encode()


@dataclass
class SearchInput:
    """A web search. A provider that cannot honour region or freshness must
    fail rather than ignore it."""

    query: str = ""
    max_results: int = 0
    include_date: bool = False
    region: str = ""
    freshness: str = ""


@dataclass
class SearchResult:
    title: str
    url: str
    snippet: str = ""
    content: str = ""
    age: str = ""
    published_at: Optional[datetime] = None


@dataclass
class ParseInput:
    """One document to parse: its bytes, or a URL. file_type is the
    lower-case extension without the dot."""

    file_name: str = ""
    file_type: str = ""
    content: bytes = b""
    url: str = ""
    title: str = ""


@dataclass
class ParsedImage:
    """An image the Markdown references as ![](original_ref)."""

    original_ref: str
    data: bytes
    mime_type: str = ""


@dataclass
class ParseOutput:
    """A document as Markdown; WeKnora chunks it."""

    markdown: str
    images: List[ParsedImage] = field(default_factory=list)
    metadata: Optional[Dict[str, str]] = None


@dataclass
class KVEntry:
    key: str
    value: Any = None
    expires_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None


@dataclass
class KVList:
    entries: List[KVEntry] = field(default_factory=list)
    next: str = ""
