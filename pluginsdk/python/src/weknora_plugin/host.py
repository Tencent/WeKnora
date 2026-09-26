"""The Host API client: how a plugin calls back into WeKnora during a call."""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from datetime import timedelta
from typing import Any, Optional, Tuple, Union

from .protocol import HOST_DATA_SOURCES_PATH, HOST_KV_LIST_PATH, HOST_KV_PATH, ErrorCode, PluginError
from .types import DataSourceInfo, KVEntry, KVList, SyncStarted, from_wire


def _encode(value: Any) -> bytes:
    # UTF-8 as is, without spaces: the store counts bytes, and \uXXXX escapes
    # would make CJK text cost six bytes a character instead of three. Only
    # text UTF-8 cannot carry (lone surrogates) falls back to escapes.
    try:
        return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    except UnicodeEncodeError:
        return json.dumps(value, separators=(",", ":")).encode()


def kv_value_size(value: Any) -> int:
    """The bytes value takes in the key-value store, as Host.kv_put sends
    it; the store refuses more than KV_MAX_VALUE_BYTES."""
    return len(_encode(value))


class Host:
    """Reaches the Host API with the short-lived token of one call. Use it
    within the call; the token expires in minutes."""

    def __init__(self, url: str, token: str, timeout: float = 30) -> None:
        self._base = url.rstrip("/")
        self._token = token
        self._timeout = timeout

    def _do(self, method: str, path: str, query: Optional[dict] = None, body: Any = None) -> Any:
        url = self._base + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        data = None
        headers = {"Authorization": "Bearer " + self._token}
        if body is not None:
            data = _encode(body)
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(url, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=self._timeout) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                err = json.loads(raw)["error"]
                if err.get("code"):
                    raise PluginError.from_wire(err) from None
            except (ValueError, KeyError, TypeError):
                pass
            raise PluginError(ErrorCode.INTERNAL, f"host api answered HTTP {e.code}") from None
        except (urllib.error.URLError, OSError) as e:
            raise PluginError(ErrorCode.UNAVAILABLE, f"host api: {e}") from None
        return json.loads(raw) if raw else None

    def kv_entry(self, key: str) -> Optional[KVEntry]:
        """Reads a key with its metadata, or None when it does not exist."""
        try:
            return from_wire(KVEntry, self._do("GET", HOST_KV_PATH, {"key": key}))
        except PluginError as e:
            if e.code == ErrorCode.NOT_FOUND:
                return None
            raise

    def kv_get(self, key: str, default: Any = None) -> Any:
        """Reads a key's value, or default when it does not exist."""
        entry = self.kv_entry(key)
        return default if entry is None else entry.value

    def kv_put(self, key: str, value: Any, ttl: Union[float, timedelta] = 0) -> None:
        """Stores a JSON value; ttl (seconds or a timedelta) 0 keeps it
        until deleted."""
        if isinstance(ttl, timedelta):
            ttl = ttl.total_seconds()
        body: dict = {"key": key, "value": value}
        if ttl:
            body["ttlSeconds"] = int(ttl)
        self._do("PUT", HOST_KV_PATH, body=body)

    def kv_delete(self, key: str) -> None:
        """Removes a key; a missing key is not an error."""
        self._do("DELETE", HOST_KV_PATH, {"key": key})

    def kv_list(self, prefix: str = "", after: str = "", limit: int = 0) -> KVList:
        """A page of keys with prefix, in key order after the given key;
        pass the result's next as after for the following page."""
        q = {"prefix": prefix, "after": after}
        if limit > 0:
            q["limit"] = str(limit)
        return from_wire(KVList, self._do("GET", HOST_KV_LIST_PATH, q))

    def kv_items(self, prefix: str = "") -> "Tuple[KVEntry, ...]":
        """Every entry with prefix, following pages."""
        out, after = [], ""
        while True:
            page = self.kv_list(prefix, after)
            out.extend(page.entries)
            if not page.next:
                return tuple(out)
            after = page.next

    def data_sources(self) -> "Tuple[DataSourceInfo, ...]":
        """The workspace's data sources of the plugin's connectors (scope
        "datasources")."""
        out = self._do("GET", HOST_DATA_SOURCES_PATH) or {}
        return tuple(from_wire(DataSourceInfo, d) for d in out.get("dataSources") or [])

    def sync_data_source(self, id: str) -> SyncStarted:
        """Starts an incremental sync of one of them, unless one is already
        running; status is "queued" or "running" (scope "datasources")."""
        path = HOST_DATA_SOURCES_PATH + "/" + urllib.parse.quote(id, safe="") + "/sync"
        return from_wire(SyncStarted, self._do("POST", path, body={}))
