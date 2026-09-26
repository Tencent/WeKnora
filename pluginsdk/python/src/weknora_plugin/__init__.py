"""Build WeKnora code plugins in Python.

    from weknora_plugin import Plugin, SearchResult

    plugin = Plugin("acme.search", "1.0.0")

    @plugin.web_search("web")
    def search(call, query):
        return [SearchResult(title="WeKnora", url="https://github.com/Tencent/WeKnora")]

    if __name__ == "__main__":
        plugin.serve()

Started by the WeKnora plugin host, the plugin listens where the host says
and accepts only the host's token. Run on its own (a remote plugin), it
listens on WEKNORA_PLUGIN_ADDR and checks request signatures with
WEKNORA_PLUGIN_SECRET. The package uses only the standard library.
"""

from .host import Host
from .plugin import Call, Plugin, Stream, StreamClosed
from .protocol import API_VERSION, PROTOCOL_VERSION, ErrorCode, PluginError, invalid_config
from .types import (
    FETCH_FULL,
    FETCH_INCREMENTAL,
    EVENT_CHAT_ANSWERED,
    EVENT_KNOWLEDGE_DELETED,
    EVENT_KNOWLEDGE_FAILED,
    EVENT_KNOWLEDGE_INGESTED,
    ConnectorConfig,
    Cursor,
    DataSourceInfo,
    EventDelivery,
    FetchedItem,
    FetchInput,
    Option,
    OptionsInput,
    KVEntry,
    KVList,
    ParsedImage,
    ParseInput,
    ParseOutput,
    Resource,
    SearchInput,
    SearchResult,
    SyncStarted,
    ToolContent,
    ToolResult,
    UIRequest,
    UIResponse,
    WebhookRequest,
    WebhookResponse,
)

__version__ = "0.1.0"

__all__ = [
    "API_VERSION",
    "PROTOCOL_VERSION",
    "FETCH_FULL",
    "FETCH_INCREMENTAL",
    "Call",
    "ConnectorConfig",
    "Cursor",
    "EVENT_CHAT_ANSWERED",
    "EVENT_KNOWLEDGE_DELETED",
    "EVENT_KNOWLEDGE_FAILED",
    "EVENT_KNOWLEDGE_INGESTED",
    "EventDelivery",
    "ErrorCode",
    "FetchInput",
    "FetchedItem",
    "Host",
    "KVEntry",
    "KVList",
    "Option",
    "OptionsInput",
    "ParseInput",
    "ParseOutput",
    "ParsedImage",
    "Plugin",
    "PluginError",
    "Resource",
    "SearchInput",
    "DataSourceInfo",
    "SyncStarted",
    "ToolContent",
    "ToolResult",
    "SearchResult",
    "Stream",
    "StreamClosed",
    "UIRequest",
    "UIResponse",
    "WebhookRequest",
    "WebhookResponse",
    "invalid_config",
]
