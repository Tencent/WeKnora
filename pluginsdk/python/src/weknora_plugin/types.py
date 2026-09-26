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


_FRACTION = re.compile(r"\.(\d+)")


def parse_time(value: Any) -> Optional[datetime]:
    """Reads an RFC 3339 time as Go writes it (nanoseconds, a Z suffix)."""
    if not value:
        return None
    if isinstance(value, datetime):
        return value
    # Go writes 0 to 9 fraction digits; Python before 3.11 reads exactly 3
    # or 6.
    s = _FRACTION.sub(lambda m: "." + m.group(1)[:6].ljust(6, "0"), str(value).replace("Z", "+00:00"), count=1)
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


@dataclass
class DataSourceInfo:
    """One data source of the plugin's connectors in the workspace."""

    id: str
    name: str = ""
    knowledge_base_id: str = ""
    connector: str = ""
    resource_ids: List[str] = field(default_factory=list)
    status: str = ""
    last_sync_at: Optional[datetime] = None


@dataclass
class SyncStarted:
    """What became of a sync request: "queued", or "running" when a sync
    had already started."""

    status: str
    sync_log_id: str = ""


@dataclass
class UIRequest:
    """A request one of the plugin's pages made through the WeKnora bridge.
    mount is "<point>/<id>" ("pages/links"); method and path are the page's
    own routing; role is the caller's workspace role."""

    mount: str = ""
    method: str = ""
    path: str = ""
    body: Any = None
    role: str = ""


@dataclass
class UIResponse:
    """What the page receives: an HTTP-style status and a JSON body."""

    body: Any = None
    status: int = 200


# Event types a plugin can subscribe to in permissions.events.
EVENT_KNOWLEDGE_INGESTED = "knowledge.ingested"
EVENT_KNOWLEDGE_FAILED = "knowledge.failed"
EVENT_KNOWLEDGE_DELETED = "knowledge.deleted"
EVENT_CHAT_ANSWERED = "chat.answered"


@dataclass
class EventDelivery:
    """One event. Deliveries repeat (at least once): be idempotent on id.
    data is the event's payload as a dict (see the protocol for each type)."""

    id: str = ""
    type: str = ""
    occurred_at: Optional[datetime] = None
    attempt: int = 1
    data: Any = None


@dataclass
class WebhookRequest:
    """An inbound call to one of the plugin's webhooks. body is the raw
    bytes; headers carry one value each."""

    method: str = ""
    path: str = "/"
    query: str = ""
    headers: Dict[str, str] = field(default_factory=dict)
    body: bytes = b""

    def json(self) -> Any:
        import json as _json

        return _json.loads(self.body or b"null")


@dataclass
class WebhookResponse:
    """What the caller receives. A str body is sent as UTF-8."""

    status: int = 200
    content_type: str = ""
    body: Optional[Union[bytes, str]] = None

    def __post_init__(self) -> None:
        if isinstance(self.body, str):
            self.body = self.body.encode()


@dataclass
class OptionsInput:
    """Asks for one form field's choices (x-options). field is its dotted
    path; scope is system, tenant or instance; contribution names the
    instance's contribution ("connectors/jira"); query is what the user
    typed."""

    field: str = ""
    scope: str = ""
    contribution: str = ""
    query: str = ""


@dataclass
class Option:
    """One choice of a form field."""

    value: Any
    label: str
    description: str = ""


@dataclass
class ToolContent:
    """One content block of a tool result: text, or an image (base64
    data)."""

    type: str = "text"
    text: str = ""
    data: str = ""
    mime_type: str = ""


@dataclass
class ToolResult:
    """What a tool returns. The model reads content; structured_content is
    data for the tool's result view (toolViews in plugin.yaml). is_error
    marks a failure the model should see."""

    content: List[ToolContent] = field(default_factory=list)
    structured_content: Any = None
    is_error: bool = False

    @staticmethod
    def text(text: str) -> "ToolResult":
        return ToolResult(content=[ToolContent(text=text)])

    @staticmethod
    def structured(data: Any, text: str = "") -> "ToolResult":
        """Data for the result view, and text for the model (the data as
        JSON when text is empty)."""
        if not text:
            import json

            text = json.dumps(to_wire(data), ensure_ascii=False, separators=(",", ":"))
        return ToolResult(content=[ToolContent(text=text)], structured_content=data)

    @staticmethod
    def error(message: str) -> "ToolResult":
        return ToolResult(content=[ToolContent(text=message)], is_error=True)
