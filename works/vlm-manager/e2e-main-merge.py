#!/usr/bin/env python3
"""S1 config round-trip e2e for the feat/vlm-manager <- upstream/main merge.

Verifies that the 5-hand-edited conflict resolutions (image_vector_enabled vs
image_pipeline) survive a real KB create -> read -> update -> read cycle
through the running app (rebuilt from the vlm-manager branch). Uses the real
qwen3.8-flash VLM model as the KB's vlm_config so the test exercises the
actual merged code path.

Run: python3 works/vlm-manager/e2e-main-merge.py
Requires: the app container rebuilt from feat/vlm-manager, up on :8080.
"""
import base64
import hashlib
import hmac
import json
import os
import re
import sys
import time
import urllib.request
import urllib.error
import uuid

REPO = "/Users/zhanghccn/WeKnora-mba/WeKnora"
APP = "http://localhost:8080"
USER_ID = "bebb1f6d-2cea-4bfe-978e-692c65865f17"   # zhanghccn@163.com
TENANT_ID = 10000
VLM_MODEL_ID = "c6f507f3-133a-44e4-8174-228036326241"  # qwen3.8-flash-next-iq3_xxs
TTL = 3600


def b64url(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode("ascii")


def load_jwt_secret():
    path = os.path.join(REPO, ".env")
    secret = None
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            m = re.match(r"\s*JWT_SECRET\s*=\s*(.+)\s*$", line)
            if m:
                secret = m.group(1).strip().strip('"').strip("'")
    if not secret:
        sys.exit("JWT_SECRET not found in .env")
    return secret


def mint_jwt(secret: str) -> str:
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {
        "user_id": USER_ID,
        "tenant_id": TENANT_ID,
        "type": "access",
        "iat": now,
        "exp": now + TTL,
    }
    signing = (b64url(json.dumps(header).encode()) + "." +
               b64url(json.dumps(payload).encode())).encode("ascii")
    sig = hmac.new(secret.encode(), signing, hashlib.sha256).digest()
    return signing.decode("ascii") + "." + b64url(sig)


def insert_token(jwt: str, secret: None):
    import subprocess
    expires = time.strftime("%Y-%m-%d %H:%M:%S+00", time.gmtime(int(time.time()) + TTL))
    tid = str(uuid.uuid4())
    sql = (
        "INSERT INTO auth_tokens (id, user_id, token, token_type, expires_at, is_revoked) "
        f"VALUES ('{tid}', '{USER_ID}', '{jwt}', 'bearer', '{expires}', false);"
    )
    cmd = ["docker", "exec", "WeKnora-postgres", "psql", "-U", "postgres", "-d", "WeKnora",
           "-c", sql]
    subprocess.run(cmd, check=True, capture_output=True, text=True)


def revoke_token(jwt: str):
    import subprocess
    sql = f"UPDATE auth_tokens SET is_revoked=true WHERE token='{jwt}';"
    cmd = ["docker", "exec", "WeKnora-postgres", "psql", "-U", "postgres", "-d", "WeKnora",
           "-c", sql]
    subprocess.run(cmd, check=True, capture_output=True, text=True)


def api(method: str, path: str, token: str, body=None):
    url = APP + path
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", "Bearer " + token)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, json.loads(r.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        detail = e.read().decode("utf-8")
        return e.code, detail


def get_ipc(resp):
    # The KB lives under resp["data"]; fall back to top-level for safety.
    kb = resp.get("data") if isinstance(resp, dict) and "data" in resp else resp
    cfg = kb.get("image_processing_config") or kb.get("image_processing")
    return cfg or {}


def main():
    secret = load_jwt_secret()
    jwt = mint_jwt(secret)
    insert_token(jwt, secret)
    try:
        name = "vlm-mgr-e2e-" + str(int(time.time()))
        # S1a: create with BOTH image_vector_enabled and image_pipeline set.
        create_body = {
            "name": name,
            "description": "e2e for main-merge conflict resolution",
            "vlm_config": {"enabled": True, "model_id": VLM_MODEL_ID},
            "image_processing_config": {
                "image_attrs_enabled": True,
                "image_actions": {"ocr": {"on": [{"prop": "contain.text", "is": "block"}],
                                          "on_unobserved": False}},
                "image_pipeline": "smartocr",
                "image_pipeline_params": {"enable_caption": False, "enable_ocr": True},
                "image_vector_enabled": True,
            },
        }
        st, resp = api("POST", "/api/v1/knowledge-bases", jwt, create_body)
        if st >= 400:
            print(f"CREATE FAILED {st}: {resp}")
            sys.exit(1)
        kb_id = resp.get("id") or (resp.get("data") or {}).get("id")
        print(f"CREATE ok id={kb_id} status={st}")

        st, got = api("GET", f"/api/v1/knowledge-bases/{kb_id}", jwt)
        ipc = get_ipc(got)
        print("GET full:", json.dumps(got, ensure_ascii=False)[:2000])
        print("GET ipc:", json.dumps(ipc, ensure_ascii=False))
        assert ipc.get("image_vector_enabled") is True, "image_vector_enabled lost"
        assert ipc.get("image_pipeline") == "smartocr", "image_pipeline lost"
        assert ipc.get("image_pipeline_params", {}).get("enable_ocr") is True, "params lost"
        assert ipc.get("image_attrs_enabled") is True, "image_attrs_enabled lost"
        print("S1a PASS: both image_vector_enabled and image_pipeline round-tripped")

        # S1b: update the image config through the UPDATE body shape. The update
        # endpoint nests image_processing_config under `config` (unlike create,
        # which accepts it top-level), so we must mirror the frontend: reuse the
        # current config and flip only image_vector_enabled off. Clearing the
        # pipeline to "" while attrs stay enabled is rejected by
        # validateImagePipelineConfig by design, so we keep a valid pipeline.
        data = got.get("data", got) if isinstance(got, dict) else got
        cfg = {}
        for key in ("chunking_config", "indexing_strategy", "faq_config",
                    "wiki_config", "auto_tag_config", "profile_config"):
            if key in data and data[key] is not None:
                cfg[key] = data[key]
        ipc_off = dict(get_ipc(got))  # copy current image config
        ipc_off["image_vector_enabled"] = False
        cfg["image_processing_config"] = ipc_off
        update_body = {
            "name": name,
            "description": data.get("description", ""),
            "config": cfg,
        }
        st, upd_resp = api("PUT", f"/api/v1/knowledge-bases/{kb_id}", jwt, update_body)
        print(f"PUT status={st} resp={json.dumps(upd_resp, ensure_ascii=False)[:800]}")
        if st >= 400:
            print(f"S1b PUT FAILED {st}: {upd_resp}")
            sys.exit(1)
        st, got2 = api("GET", f"/api/v1/knowledge-bases/{kb_id}", jwt)
        ipc2 = get_ipc(got2)
        print("GET after update ipc:", json.dumps(ipc2, ensure_ascii=False))
        # image_vector_enabled is json-omitempty by design: a stored `false`
        # serializes to an ABSENT key (see types.ImageProcessingConfig,
        # ImageVectorEnabled). Absent == off == functionally false, so the
        # correct post-update assertion is "not True", not "is False".
        assert ipc2.get("image_vector_enabled") is not True, "vector not cleared on update"
        assert ipc2.get("image_pipeline") == "smartocr", "pipeline lost on update"
        assert ipc2.get("image_pipeline_params", {}).get("enable_ocr") is True, "params lost on update"
        assert ipc2.get("image_attrs_enabled") is True, "attrs lost on update"
        print("S1b PASS: update wrote image_processing_config and preserved the merge-conflict fields")

        # cleanup
        st, _ = api("DELETE", f"/api/v1/knowledge-bases/{kb_id}", jwt)
        print(f"DELETE status={st}")
        print("ALL S1 PASSED")
    finally:
        revoke_token(jwt)
        print("token revoked")


if __name__ == "__main__":
    main()
