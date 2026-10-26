#!/usr/bin/env python3
"""Register a frozen recovery manifest for tracking; never submit parse tasks."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def registration(manifest, expected_count=None, name=None):
    kb_id = manifest.get("kb_id")
    entries = manifest.get("entries")
    if not isinstance(kb_id, str) or not kb_id.strip() or not isinstance(entries, list):
        raise ValueError("manifest requires kb_id and entries")
    ids = set()
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError("each entry must be an object")
        identifier = entry.get("id")
        if not isinstance(identifier, str) or not identifier.strip() or identifier != identifier.strip():
            raise ValueError("each entry requires a nonblank id without surrounding whitespace")
        if any(c in identifier for c in "\r\n"):
            raise ValueError("ids must not contain line breaks")
        if entry.get("knowledge_base_id", kb_id) != kb_id:
            raise ValueError("manifest contains members of another knowledge base")
        ids.add(identifier)
    ordered = sorted(ids)
    if not 1 <= len(ordered) <= 50000:
        raise ValueError("manifest must contain between 1 and 50000 unique ids")
    if expected_count is not None and len(ordered) != expected_count:
        raise ValueError(f"expected {expected_count} unique members, got {len(ordered)}")
    digest = hashlib.sha256(("\n".join(ordered) + "\n").encode("utf-8")).hexdigest()
    body = {"kb_id": kb_id, "ids": ordered, "mode": "tracking_only", "manifest_sha256": digest}
    if name:
        body["name"] = name
    return body


def register(base_url, token, body, timeout=60):
    parsed = urlsplit(base_url)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.password:
        raise ValueError("base URL must be an HTTP(S) URL without embedded credentials")
    if parsed.query or parsed.fragment or parsed.path.rstrip("/") not in ("", "/api/v1"):
        raise ValueError("base URL must be the server root or /api/v1")
    endpoint = base_url.rstrip("/")
    if not endpoint.endswith("/api/v1"):
        endpoint += "/api/v1"
    endpoint += "/knowledge/reparse-batches"
    request = Request(endpoint, data=json.dumps(body).encode("utf-8"), method="POST",
                      headers={"Content-Type": "application/json", "X-API-Key": token})
    # Do not automatically retry network failures or redirect credentials to another host.
    with build_opener(NoRedirect).open(request, timeout=timeout) as response:
        result = json.load(response)
    if not result.get("success") or not isinstance(result.get("data"), dict) or not result["data"].get("id"):
        raise ValueError("registration returned an invalid batch response")
    return result["data"]["id"]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("--expected-count", type=int)
    parser.add_argument("--name")
    parser.add_argument("--base-url")
    parser.add_argument("--dry-run", action="store_true", help="validate locally; no HTTP request")
    args = parser.parse_args(argv)
    try:
        body = registration(json.loads(args.manifest.read_text(encoding="utf-8-sig")), args.expected_count, args.name)
        if args.dry_run:
            print(json.dumps({"mode": body["mode"], "count": len(body["ids"]), "manifest_sha256": body["manifest_sha256"], "dry_run": True}))
            return 0
        if not args.base_url:
            raise ValueError("--base-url is required unless --dry-run is used")
        token = os.environ.get("WEKNORA_API_KEY", "")
        if not token:
            raise ValueError("WEKNORA_API_KEY must be supplied through the environment")
        batch_id = register(args.base_url, token, body)
        print(json.dumps({"batch_id": batch_id, "count": len(body["ids"]), "manifest_sha256": body["manifest_sha256"]}))
        return 0
    except HTTPError as exc:
        print(f"Registration failed: HTTP {exc.code}; no parsing request was sent", file=sys.stderr)
        exc.close()
    except URLError:
        print("Registration outcome is unknown after network failure; rerun the same frozen manifest to recover the same batch", file=sys.stderr)
    except (ValueError, OSError) as exc:
        # Manifest text, server response bodies and credentials are deliberately not logged.
        print(f"Import failed: {type(exc).__name__}; check manifest structure, count, URL and API key configuration", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
