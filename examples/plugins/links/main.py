"""A WeKnora plugin with pages, written with the Python SDK: a toolbox page
listing the workspace's links, a settings section where admins edit them,
and a tab on each knowledge base with links about it. The pages run
sandboxed in the browser and reach this code only through the bridge
(wk.get / wk.put), which WeKnora relays as UI requests."""

from __future__ import annotations

import re
from urllib.parse import urlparse

from weknora_plugin import KV_MAX_VALUE_BYTES, ErrorCode, Plugin, PluginError, UIResponse, kv_value_size

plugin = Plugin("weknora-examples.links", "1.1.1")

MAX_LINKS = 200
EDITORS = {"contributor", "admin", "owner"}
KB_PATH = re.compile(r"^/kb/([A-Za-z0-9_-]{1,64})/links$")


def clean_links(value) -> list:
    """Validates links from a page: [{title, url}], http(s) only."""
    if not isinstance(value, list) or len(value) > MAX_LINKS:
        raise ValueError(f"send a list of at most {MAX_LINKS} links")
    out = []
    for item in value:
        if not isinstance(item, dict):
            raise ValueError("each link is an object with a title and a url")
        title = str(item.get("title") or "").strip()[:120]
        url = str(item.get("url") or "").strip()
        parsed = urlparse(url)
        if parsed.scheme not in ("http", "https") or not parsed.netloc:
            raise ValueError(f"{url or 'an empty url'} is not an http(s) link")
        out.append({"title": title or parsed.netloc, "url": url[:2000]})
    # A list is one value in the store, which holds 64 KB: long URLs or
    # titles fill it before the count does.
    size = kv_value_size(out)
    if size > KV_MAX_VALUE_BYTES:
        raise ValueError(
            f"these links take {size // 1024} KB, over the {KV_MAX_VALUE_BYTES // 1024} KB they can take together; "
            "remove some or shorten them"
        )
    return out


def store(call):
    host = call.host()
    if host is None:
        raise PluginError(ErrorCode.UNAVAILABLE, "the Host API is not available to this plugin")
    return host


def handle(call, req) -> UIResponse:
    host = store(call)
    if req.path == "/links":
        if req.method == "GET":
            return UIResponse(body=host.kv_get("links", []))
        # Only the settings section edits the workspace's links; WeKnora
        # lets admins alone open it.
        if req.method == "PUT" and req.mount == "settingsSections/manage":
            return save(host, "links", req.body)
    m = KB_PATH.match(req.path)
    if m and req.mount == "kbTabs/links":
        key = f"kb:{m.group(1)}"
        if req.method == "GET":
            return UIResponse(body=host.kv_get(key, []))
        if req.method == "PUT":
            if req.role not in EDITORS:
                return UIResponse(status=403, body={"error": "only editors of the workspace can change links"})
            return save(host, key, req.body)
    return UIResponse(status=404, body={"error": f"no {req.method} {req.path} here"})


def save(host, key, body) -> UIResponse:
    try:
        links = clean_links(body)
    except ValueError as e:
        return UIResponse(status=400, body={"error": str(e)})
    host.kv_put(key, links)
    return UIResponse(body=links)


plugin.ui(handle)

if __name__ == "__main__":
    plugin.serve()
