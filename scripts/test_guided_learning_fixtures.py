"""Offline fixture tests; all runtime files and credentials are synthetic.

python3 -B -m unittest discover -s scripts -p 'test_guided_learning_fixtures.py' -v
"""

from collections import Counter
from contextlib import ExitStack, redirect_stderr, redirect_stdout
import copy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import socket
import stat
import sys
import tempfile
import threading
import unittest
from unittest import mock
import urllib.error
import urllib.parse
import uuid

import guided_learning_fixtures as helper


SCRIPTS = Path(__file__).resolve().parent
MODEL = {"name": "offline-fixture-model",
         "base_url": "https://model.example.invalid/v1",
         "api_key": "synthetic-model-secret-not-a-real-key"}
MODEL_ENV = {"MODEL_NAME": MODEL["name"], "MODEL_BASE_URL": MODEL["base_url"],
             "MODEL_API_KEY": MODEL["api_key"]}


def load_script(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


seed_script = load_script("offline_seed_guided_learning", "seed-guided-learning.py")
api_script = load_script("offline_test_guided_learning_api", "test-guided-learning-api.py")


def new_id():
    return str(uuid.uuid4())


def envelope(data, status=200):
    return status, {"success": True, "data": data}, {"Cache-Control": "no-store"}


class LoopbackServer:
    def __init__(self, dispatch):
        self.dispatch = dispatch
        self.requests = []
        self.errors = []
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def handle_request(self):
                try:
                    raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                    request = {"method": self.command, "path": self.path,
                               "headers": dict(self.headers),
                               "body": json.loads(raw) if raw else None}
                    owner.requests.append(request)
                    status, value, headers = owner.dispatch(request)
                    body = value if isinstance(value, bytes) else json.dumps(value).encode()
                except Exception as error:
                    owner.errors.append(error)
                    status, body, headers = 500, b'{"success": false}', {}
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                for key, value in headers.items():
                    self.send_header(key, value)
                self.end_headers()
                self.wfile.write(body)

            do_GET = do_POST = do_PUT = do_DELETE = handle_request

            def log_message(self, _format, *args):
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.base = "http://127.0.0.1:" + str(self.server.server_port)
        self.thread = threading.Thread(
            target=self.server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.thread.join(timeout=5)
        self.server.server_close()


class FixtureAPI:
    """Small tenant-scoped API, with real envelopes and bare Wiki DTOs."""

    def __init__(self):
        self.users = {}
        self.tokens = {}
        self.interrupt_registration = False

    def __call__(self, request):
        method, body = request["method"], request["body"]
        url = urllib.parse.urlsplit(request["path"])
        assert url.path.startswith("/api/v1/")
        parts = url.path.removeprefix("/api/v1/").split("/")
        if parts == ["auth", "register"] and method == "POST":
            assert body["email"] not in self.users, "duplicate registration"
            tenant = len(self.users) + 1
            user = {**body, "id": new_id(), "tenant_id": tenant,
                    "token": "synthetic-bearer-token-" + str(tenant),
                    "models": {}, "bases": {}, "documents": {}, "chunks": {},
                    "pages": {}, "enabled": False}
            self.users[user["email"]] = user
            self.tokens["Bearer " + user["token"]] = user
            if self.interrupt_registration:
                self.interrupt_registration = False
                return 503, {"success": False, "error": {"code": "interrupted"}}, {}
            return 201, {"success": True, "user": {
                key: user[key] for key in ("id", "username", "email", "tenant_id")}}, {}
        if parts == ["auth", "login"] and method == "POST":
            user = self.users.get(body["email"])
            if not user or user["password"] != body["password"]:
                return 401, {"success": False, "error": {"code": "unauthorized"}}, {}
            return 200, {"success": True, "token": user["token"],
                         "user": {key: user[key] for key in ("id", "username", "email", "tenant_id")},
                         "active_tenant": {"id": user["tenant_id"]}}, {}

        user = self.tokens.get(request["headers"].get("Authorization"))
        assert user is not None, "authenticated endpoint requires an owned token"
        if parts == ["models"]:
            if method == "GET":
                return envelope(list(user["models"].values()))
            if method == "POST":
                assert body["parameters"]["api_key"] == MODEL["api_key"]
                model = {**body, "id": new_id(), "tenant_id": user["tenant_id"],
                         "parameters": {key: value for key, value in body["parameters"].items()
                                        if key != "api_key"}}
                user["models"][model["id"]] = model
                return envelope(model, 201)
        if parts == ["knowledge-bases"]:
            if method == "GET":
                return envelope(list(user["bases"].values()))
            if method == "POST":
                assert body["id"] not in user["bases"], "duplicate knowledge base"
                kb = {**body, "tenant_id": user["tenant_id"]}
                user["bases"][body["id"]] = kb
                return envelope(kb, 201)
        if parts[0] == "knowledge-bases" and len(parts) >= 3:
            kb_id = parts[1]
            assert kb_id in user["bases"], "foreign knowledge base"
            if parts[2:] == ["knowledge"] and method == "GET":
                query = urllib.parse.parse_qs(url.query)
                assert set(query) == {"page", "page_size"} and query["page_size"] == ["100"]
                page = int(query["page"][0])
                assert page > 0
                rows = [doc for doc in user["documents"].values()
                        if doc["knowledge_base_id"] == kb_id]
                return 200, {"success": True, "data": rows[(page - 1) * 100:page * 100],
                             "total": len(rows), "page": page, "page_size": 100}, {}
            if parts[2:] == ["knowledge", "manual"] and method == "POST":
                assert body["status"] == "publish"
                doc = {"title": body["title"], "id": new_id(), "knowledge_base_id": kb_id,
                       "tenant_id": user["tenant_id"], "type": "manual",
                       "metadata": {"content": body["content"], "status": body["status"],
                                    "format": "markdown", "version": 1},
                       "parse_status": "completed", "enable_status": "enabled"}
                user["documents"][doc["id"]] = doc
                chunk = {"id": new_id(), "knowledge_id": doc["id"], "content": body["content"],
                         "index_status": "ready", "is_enabled": True}
                user["chunks"][doc["id"]] = [
                    chunk,
                    {**chunk, "id": new_id(), "index_status": "pending"},
                    {**chunk, "id": new_id(), "is_enabled": False},
                    {**chunk, "id": new_id(), "content": ""},
                ]
                return envelope(doc, 200)
        if parts[0] == "knowledge" and len(parts) == 2 and method == "GET":
            return envelope(user["documents"][parts[1]])
        if parts[0] == "knowledge" and parts[2:] == ["reparse"] and method == "POST":
            doc = user["documents"][parts[1]]
            doc["parse_status"] = "completed"
            user["chunks"][doc["id"]] = [
                {**chunk, "id": new_id()} for chunk in user["chunks"][doc["id"]]]
            return envelope(doc)
        if parts[0] == "chunks" and len(parts) == 2 and method == "GET":
            assert urllib.parse.parse_qs(url.query) == {"page": ["1"], "page_size": ["100"]}
            return envelope(user["chunks"][parts[1]])
        if parts[0] == "knowledgebase" and len(parts) >= 4 and parts[2] == "wiki":
            kb_id = parts[1]
            assert kb_id in user["bases"], "foreign Wiki knowledge base"
            action = parts[3]
            if action == "pages":
                if len(parts) == 4 and method == "POST":
                    key = (kb_id, body["slug"])
                    assert key not in user["pages"], "duplicate Wiki page"
                    page = {**body, "id": new_id(), "knowledge_base_id": kb_id,
                            "tenant_id": user["tenant_id"], "version": 1}
                    user["pages"][key] = page
                    return 201, page, {}
                key = (kb_id, "/".join(parts[4:]))
                page = user["pages"].get(key)
                if not page:
                    return 404, {"error": {"code": "not_found"}}, {}
                if method == "GET":
                    return 200, page, {}
                if method == "DELETE":
                    del user["pages"][key]
                    return 204, b"", {}
                if method == "PUT":
                    assert body["version"] == page["version"]
                    page.update({**body, "version": page["version"] + 1})
                    return 200, page, {}
            if action == "rebuild-links" and method == "POST":
                return 200, {"rebuilt": True}, {}
            if action == "graph" and method == "GET":
                pages = [page for (base, _slug), page in user["pages"].items() if base == kb_id]
                return 200, {"nodes": [
                    {key: page[key] for key in ("slug", "title", "page_type")} for page in pages
                ], "edges": [{"source": page["slug"], "target": link} for page in pages
                             for link in re.findall(r"\[\[([^\]]+)\]\]", page["content"])]}, {}
        if parts == ["agents", "builtin-guided-learning"]:
            agent = user.setdefault("agent", {
                "id": "builtin-guided-learning", "tenant_id": user["tenant_id"], "is_builtin": True,
                "config": {"model_id": "", "kb_selection_mode": "all", "knowledge_bases": [],
                           "allowed_tools": ["wiki_read_page", "prepare_learning_quiz"]},
            })
            if method == "PUT":
                assert set(body) == {"config"}
                agent["config"] = body["config"]
            else:
                assert method == "GET"
            return envelope(agent)
        if parts == ["learning", "settings"]:
            if method == "PUT":
                assert body == {"enabled": True}
                user["enabled"] = True
            else:
                assert method == "GET"
            return envelope({"enabled": user["enabled"]})
        if parts == ["learning", "export"] and method == "GET":
            return envelope({"settings": {"enabled": user["enabled"]},
                             "nodes": [], "quizzes": [], "attempts": []})
        raise AssertionError("unhandled mock route: " + method + " " + url.path)


class OfflineCase(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="guided-learning-tests-")
        self.addCleanup(temporary.cleanup)
        self.repo = Path(temporary.name) / "checkout"
        self.repo.mkdir(mode=0o700)
        self.root = self.repo / ".runtime"
        self.contexts = ExitStack()
        self.addCleanup(self.contexts.close)
        self.contexts.enter_context(mock.patch.dict(os.environ, {}, clear=True))
        self.contexts.enter_context(mock.patch.object(helper, "REPO", self.repo))
        self.contexts.enter_context(mock.patch.object(api_script, "REPO", self.repo))
        self.contexts.enter_context(mock.patch.object(api_script, "RUNTIME", self.root))
        self.contexts.enter_context(mock.patch.object(api_script, "API", helper.DEFAULT_API))
        self.stdout, self.stderr = io.StringIO(), io.StringIO()
        self.allowed_addresses = set()
        connect = socket.create_connection

        def offline_connect(address, *args, **kwargs):
            if address not in self.allowed_addresses:
                raise AssertionError("offline tests forbid non-test network connections")
            return connect(address, *args, **kwargs)

        self.contexts.enter_context(mock.patch.object(socket, "create_connection", side_effect=offline_connect))

    def serve(self, dispatch):
        server = LoopbackServer(dispatch)
        self.allowed_addresses.add(("127.0.0.1", server.server.server_port))

        def cleanup():
            server.close()
            self.assertFalse(server.thread.is_alive(), "mock server did not stop")
            self.assertEqual(server.errors, [], "mock API handler failed")

        self.addCleanup(cleanup)
        return server

    def create_root(self):
        root = helper.runtime_root(create=True)
        helper.prepare(root)
        return root

    def write_private(self, path, text):
        path.write_text(text, encoding="utf-8")
        path.chmod(0o600)
        return path

    def read_json(self, path):
        return json.loads(path.read_text(encoding="utf-8"))

    def assert_rejected(self, reason, function, *args, **kwargs):
        with self.assertRaises(helper.SafeFailure) as caught:
            function(*args, **kwargs)
        self.assertEqual(str(caught.exception), reason)
        self.assertNotIn(MODEL["api_key"], str(caught.exception))

    def run_seed(self, *args):
        argv = ["seed-guided-learning.py", *args, "--runtime-dir", str(self.root)]
        with mock.patch.object(sys, "argv", argv), redirect_stdout(self.stdout), redirect_stderr(self.stderr):
            return seed_script.main()

    def run_api(self, *args):
        argv = ["test-guided-learning-api.py", *args, "--runtime-dir", str(self.root)]
        with mock.patch.object(sys, "argv", argv), redirect_stdout(self.stdout), redirect_stderr(self.stderr):
            return api_script.main()

    def seed_mock(self):
        api = FixtureAPI()
        server = self.serve(api)
        with mock.patch.dict(os.environ, MODEL_ENV, clear=True):
            self.run_seed("seed", "--consent-create-fixtures", "--api-base", server.base)
        return api, server

    def finished_state(self):
        self.create_root()
        state = seed_script.read_state(
            self.root, helper.DEFAULT_API, helper.DEFAULT_FRONTEND, create=True)
        state["model"] = {key: MODEL[key] for key in ("name", "base_url")}
        for tenant, user in enumerate(state["users"].values(), 1):
            user.update(user_id=new_id(), tenant_id=tenant, model_id=new_id())
            for fixture in seed_script.FIXTURES:
                user["documents"][fixture["slug"]] = new_id()
                user["pages"][fixture["slug"]] = {"page_id": new_id(), "chunk_ids": [new_id()]}
        manifest = {"api_base": state["api_base"], "fixtures": {
            label: {key: copy.deepcopy(user[key]) for key in
                    ("tenant_id", "knowledge_base_id", "model_id", "documents", "pages")}
            for label, user in state["users"].items()}}
        helper.save_json(self.root / "seed-accounts.json", state)
        helper.save_json(self.root / "evidence/seed.json", manifest)
        return state, manifest


class RuntimeAndModelTests(OfflineCase):
    def test_runtime_defaults_and_relative_environment_selection_stay_in_temporary_repo(self):
        self.assertEqual(self.create_root(), self.root)
        with mock.patch.dict(os.environ, {"GL_RUNTIME_DIR": ".runtime/offline"}):
            selected = helper.runtime_root(create=True)
            self.assertEqual(selected, self.root / "offline")
            self.assertEqual(helper.runtime_root(self.root), self.root)
        for path in (self.root, selected, self.root / "evidence", self.root / "state"):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700)

    def test_runtime_missing_and_escape_paths_are_rejected(self):
        self.assert_rejected("fixtures_missing_run_seed_first", helper.runtime_root)
        for path in (self.repo, self.repo / ".runtime-other", ".runtime/../outside"):
            with self.subTest(path=path):
                self.assert_rejected("runtime_must_be_under_checkout_runtime",
                                     helper.runtime_root, path, create=True)
        self.assertFalse(self.root.exists())

    def test_runtime_rejects_symlinked_root_and_descendants(self):
        target = self.repo / "outside"
        target.mkdir(mode=0o700)
        self.root.symlink_to(target, target_is_directory=True)
        self.assert_rejected("runtime_directory_not_owned_or_writable", helper.runtime_root)
        self.root.unlink()
        self.create_root()
        (self.root / "linked").symlink_to(target, target_is_directory=True)
        self.assert_rejected("runtime_directory_not_owned_or_writable",
                             helper.runtime_root, self.root / "linked")
        self.assertEqual(list(target.iterdir()), [])

    def test_runtime_rejects_writable_and_foreign_owned_directories(self):
        self.create_root()
        for mode in (0o720, 0o702, 0o777):
            with self.subTest(mode=oct(mode)):
                self.root.chmod(mode)
                self.assert_rejected("runtime_directory_not_owned_or_writable", helper.runtime_root)
        self.root.chmod(0o700)
        with mock.patch.object(helper.os, "getuid", return_value=os.getuid() + 1):
            self.assert_rejected("runtime_directory_not_owned_or_writable", helper.runtime_root)

    def test_model_config_accepts_environment_aliases_without_a_file(self):
        aliases = {"DEEPSEEK_MODEL": MODEL["name"], "DEEPSEEK_BASE_URL": MODEL["base_url"],
                   "DEEPSEEK_API_KEY": MODEL["api_key"]}
        for values in (MODEL_ENV, aliases, {**MODEL_ENV, **aliases}):
            with self.subTest(keys=sorted(values)), mock.patch.dict(os.environ, values, clear=True):
                self.assertEqual(helper.model_config(), MODEL)
        self.assertFalse(self.root.exists())

    def test_private_model_file_is_literal_and_does_not_merge_environment(self):
        self.create_root()
        path = self.write_private(self.root / "model.env",
                                  "# offline only\nexport MODEL_NAME='offline-fixture-model'\n"
                                  "MODEL_BASE_URL=https://model.example.invalid/v1\n"
                                  "MODEL_API_KEY='literal-$NOT_EXPANDED-$(not-executed)'\n")
        with mock.patch.dict(os.environ, MODEL_ENV):
            self.assertEqual(helper.model_config(path), {
                **MODEL, "api_key": "literal-$NOT_EXPANDED-$(not-executed)"})
            self.write_private(path, "MODEL_NAME=offline-fixture-model\n")
            self.assert_rejected("set_model_name_base_url_and_api_key", helper.model_config, path)

    def test_missing_and_ambiguous_model_aliases_are_redacted(self):
        self.create_root()
        for key in MODEL_ENV:
            values = dict(MODEL_ENV)
            del values[key]
            with self.subTest(missing=key), mock.patch.dict(os.environ, values, clear=True):
                self.assert_rejected("set_model_name_base_url_and_api_key", helper.model_config)
        for alias in ("DEEPSEEK_MODEL", "DEEPSEEK_BASE_URL", "DEEPSEEK_API_KEY"):
            values = {**MODEL_ENV, alias: MODEL["api_key"] + "-conflict"}
            with self.subTest(alias=alias), mock.patch.dict(
                    os.environ, values, clear=True):
                self.assert_rejected("set_model_name_base_url_and_api_key", helper.model_config)
                path = self.write_private(self.root / "model.env", "".join(
                    key + "=" + value + "\n" for key, value in values.items()))
                self.assert_rejected("set_model_name_base_url_and_api_key", helper.model_config, path)

    def test_model_file_rejects_duplicates_shell_syntax_and_unquoted_secrets(self):
        self.create_root()
        valid = "".join(key + "=" + value + "\n" for key, value in MODEL_ENV.items())
        for suffix in ("MODEL_API_KEY=" + MODEL["api_key"] + "\n",
                       "QUOTED_SECRET='unterminated\n", "source " + MODEL["api_key"] + "\n",
                       "BAD_KEY=unquoted " + MODEL["api_key"] + "\n"):
            with self.subTest(syntax=suffix.split("=", 1)[0]):
                path = self.write_private(self.root / "model.env", valid + suffix)
                self.assert_rejected("private_model_syntax", helper.model_config, path)

    def test_model_url_rejects_insecure_and_secret_bearing_components(self):
        for url in ("http://model.example.invalid/v1",
                    "https://user:" + MODEL["api_key"] + "@model.example.invalid/v1",
                    "https://model.example.invalid/v1?key=" + MODEL["api_key"],
                    "https://model.example.invalid/v1#" + MODEL["api_key"],
                    "https://model.example.invalid/with space",
                    "https://model.example.invalid\\path", "/relative/model"):
            with self.subTest(kind=urllib.parse.urlsplit(url).scheme), mock.patch.dict(
                    os.environ, {**MODEL_ENV, "MODEL_BASE_URL": url}, clear=True):
                self.assert_rejected("model_requires_https_without_url_credentials", helper.model_config)

    def test_private_model_file_requires_owned_mode_0600(self):
        self.create_root()
        path = self.write_private(self.root / "model.env", "MODEL_API_KEY=" + MODEL["api_key"])
        for mode in (0o400, 0o640, 0o644, 0o660, 0o700):
            with self.subTest(mode=oct(mode)):
                path.chmod(mode)
                self.assert_rejected("private_file_permissions", helper.model_config, path)
        path.chmod(0o600)
        with mock.patch.object(helper.os, "getuid", return_value=os.getuid() + 1):
            self.assert_rejected("private_file_permissions", helper.model_config, path)

    def test_private_model_file_rejects_symlinks_and_hardlinks(self):
        self.create_root()
        content = "".join(key + "=" + value + "\n" for key, value in MODEL_ENV.items())
        target = self.write_private(self.root / "secret.env", content)
        self.assertEqual(helper.model_config(target), MODEL)
        path = self.root / "model.env"
        path.symlink_to(target)
        with self.assertRaises((helper.SafeFailure, OSError)):
            helper.model_config(path)
        path.unlink()
        os.link(target, path)
        self.assert_rejected("private_file_permissions", helper.model_config, path)
        self.assertEqual(target.read_text(), content)

    def test_private_file_rejects_missing_oversized_and_nonregular_input(self):
        self.create_root()
        path = self.root / "model.env"
        self.assert_rejected("private_file_missing_run_seed_or_supply_model_config",
                             helper.model_config, path)
        self.write_private(path, "x" * (1024 * 1024 + 1))
        self.assert_rejected("private_file_size", helper.model_config, path)
        path.unlink()
        os.mkfifo(path, 0o600)
        self.assert_rejected("private_file_permissions", helper.model_config, path)

    def test_atomic_state_writes_are_private_and_refuse_unsafe_existing_files(self):
        self.create_root()
        path = self.root / "seed-accounts.json"
        helper.save_json(path, {"version": 1})
        helper.save_json(path, {"version": 2})
        self.assertEqual(self.read_json(path), {"version": 2})
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(list(self.root.glob(".seed-accounts.json-*")), [])
        path.chmod(0o644)
        self.assert_rejected("private_file_permissions", helper.save_json, path, {"version": 3})
        self.assertEqual(self.read_json(path), {"version": 2})


class OriginAndStateTests(OfflineCase):
    def test_supported_loopback_origins_are_canonicalized(self):
        cases = {"http://127.0.0.1:28081/api/v1/": "http://127.0.0.1:28081",
                 "http://localhost:80/": "http://localhost",
                 "http://[::1]:28081/api/v1": "http://[::1]:28081",
                 "http://127.0.0.1": "http://127.0.0.1"}
        for value, expected in cases.items():
            with self.subTest(origin=value):
                self.assertEqual(helper.origin(value, api=True), expected)
        self.assertEqual(helper.origin("http://localhost:25173/"), "http://localhost:25173")
        self.assert_rejected("loopback_origin_required", helper.origin, "http://localhost/api/v1")

    def test_origin_rejects_nonloopback_credentials_ports_and_extra_components(self):
        cases = (None, 123, "https://localhost", "http://example.invalid", "http://0.0.0.0",
                 "http://127.0.0.2", "http://127.0.0.1.example.invalid", "//localhost",
                 " http://localhost", "http://localhost\n", "http://localhost:0",
                 "http://localhost:65536", "http://localhost:bad", "http://[::1",
                 "http://user:password@localhost", "http://localhost/path",
                 "http://localhost/api/v1/extra", "http://localhost?", "http://localhost#",
                 "http://localhost\\path")
        for value in cases:
            with self.subTest(origin=value):
                self.assert_rejected("loopback_origin_required", helper.origin, value, api=True)

    def test_strict_json_rejects_ambiguous_nonfinite_and_secret_bearing_input(self):
        for raw, reason in ((b'{"users": {}, "users": {}}', "duplicate_json_key"),
                            (b'{"value": NaN}', "nonfinite_json"),
                            (b'{"value": Infinity}', "nonfinite_json"),
                            (MODEL["api_key"], "invalid_json_redacted")):
            with self.subTest(reason=reason):
                self.assert_rejected(reason, helper.strict_json, raw)

    def test_seed_and_inspect_reject_mismatched_api_before_network(self):
        self.finished_state()
        with mock.patch.object(helper.Client, "call") as call, mock.patch.dict(os.environ, MODEL_ENV):
            self.assert_rejected("seed_belongs_to_another_api", self.run_seed,
                                 "seed", "--consent-create-fixtures", "--api-base", "http://localhost:9")
            self.assert_rejected("seed_belongs_to_another_api", self.run_api,
                                 "inspect", "--api-base", "http://localhost:9")
        call.assert_not_called()

    def test_foreign_identity_in_either_actor_is_rejected_before_network(self):
        original, _manifest = self.finished_state()
        for label in helper.ACTORS:
            for key, value in (("email", "real-user@example.com"), ("username", "foreign-user")):
                with self.subTest(actor=label, field=key):
                    state = copy.deepcopy(original)
                    state["users"][label][key] = value
                    helper.save_json(self.root / "seed-accounts.json", state)
                    with mock.patch.object(helper.Client, "call") as call, mock.patch.dict(
                            os.environ, MODEL_ENV):
                        self.assert_rejected("disposable_seed_only", self.run_seed,
                                             "seed", "--consent-create-fixtures")
                        self.assert_rejected("disposable_seed_only", self.run_api, "inspect")
                    call.assert_not_called()

    def test_shared_tenants_and_extra_users_are_rejected_before_network(self):
        original, _manifest = self.finished_state()
        shared = copy.deepcopy(original)
        shared["users"]["learner_b"]["tenant_id"] = shared["users"]["learner_a"]["tenant_id"]
        extra = copy.deepcopy(original)
        extra["users"]["foreign"] = copy.deepcopy(extra["users"]["learner_a"])
        for state, reason in ((shared, "distinct_seed_tenants"), (extra, "seed_user_scope")):
            with self.subTest(reason=reason), mock.patch.object(helper.Client, "call") as call:
                helper.save_json(self.root / "seed-accounts.json", state)
                self.assert_rejected(reason, self.run_api, "inspect")
                call.assert_not_called()

    def test_manifest_api_mismatch_is_rejected_before_login(self):
        _state, manifest = self.finished_state()
        manifest["api_base"] = "http://localhost:9"
        helper.save_json(self.root / "evidence/seed.json", manifest)
        with mock.patch.object(helper.Client, "call") as call:
            self.assert_rejected("manifest_api_mismatch", api_script.accounts)
        call.assert_not_called()

    def test_second_actor_foreign_manifest_is_rejected_before_any_login(self):
        state, manifest = self.finished_state()
        manifest["fixtures"]["learner_b"]["knowledge_base_id"] = new_id()
        helper.save_json(self.root / "evidence/seed.json", manifest)
        first = state["users"]["learner_a"]
        response = {"data": {"user": {"id": first["user_id"], "tenant_id": first["tenant_id"]},
                             "token": "synthetic-token"}}
        with mock.patch.object(helper.Client, "call", return_value=response) as call:
            self.assert_rejected("fresh_fixture_scope", api_script.accounts)
        self.assertEqual(call.call_count, 0, "validate both fixture scopes before any login")


class SeedWorkflowTests(OfflineCase):
    def test_explicit_creation_consent_precedes_config_files_and_network(self):
        with mock.patch.object(seed_script, "model_config") as config, mock.patch.object(
                helper.Client, "call") as call:
            self.assert_rejected("explicit_fixture_creation_consent_required", self.run_seed, "seed")
        config.assert_not_called()
        call.assert_not_called()
        self.assertFalse(self.root.exists())

    def test_acceptance_run_requires_separate_learner_b_consent(self):
        with mock.patch.object(helper.Client, "call") as call:
            self.assert_rejected("explicit_b_consent_required", self.run_api, "run")
        call.assert_not_called()
        self.assertFalse(self.root.exists())

    def test_seed_is_idempotent_for_two_distinct_users_and_six_cited_pages(self):
        api, server = self.seed_mock()
        state = self.read_json(self.root / "seed-accounts.json")
        manifest = self.read_json(self.root / "evidence/seed.json")
        self.assertEqual(set(state["users"]), set(helper.ACTORS))
        for field in ("user_id", "tenant_id", "knowledge_base_id", "model_id", "email", "password"):
            self.assertEqual(len({user[field] for user in state["users"].values()}), 2, field)
        self.assertEqual(len(api.users), 2)
        self.assertEqual(manifest["source_documents"], 6)
        self.assertEqual(manifest["curated_pages"], 6)
        self.assertFalse(manifest["learning_opt_in_changed"])
        all_documents, all_pages = set(), set()
        for label, saved in state["users"].items():
            user = api.users[saved["email"]]
            self.assertEqual(len(user["models"]), 1)
            self.assertEqual(len(user["bases"]), 1)
            self.assertEqual(len(user["documents"]), 3)
            self.assertEqual(len(user["pages"]), 3)
            self.assertFalse(user["enabled"])
            for fixture in seed_script.FIXTURES:
                doc_id = saved["documents"][fixture["slug"]]
                page = user["pages"][(saved["knowledge_base_id"], fixture["slug"])]
                ready = [chunk["id"] for chunk in user["chunks"][doc_id]
                         if chunk["content"] and chunk["index_status"] == "ready"
                         and chunk["is_enabled"] is True]
                self.assertEqual(page["chunk_refs"], ready)
                self.assertEqual(saved["pages"][fixture["slug"]]["chunk_ids"], ready)
                self.assertEqual(page["source_refs"], [doc_id + "|" + fixture["title"]])
                all_documents.add(doc_id)
                all_pages.add(page["id"])
            self.assertEqual(len(self.read_json(self.root / f"evidence/seed-{label}-graph.json")["nodes"]), 3)
        self.assertEqual(len(all_documents), 6)
        self.assertEqual(len(all_pages), 6)
        boundary = len(server.requests)
        with mock.patch.dict(os.environ, MODEL_ENV, clear=True):
            self.run_seed("seed", "--consent-create-fixtures")
        self.assertEqual(self.read_json(self.root / "seed-accounts.json"), state)
        self.assertEqual(self.read_json(self.root / "evidence/seed.json"), manifest)
        for request in server.requests[boundary:]:
            self.assertTrue(request["method"] == "GET" or
                            request["path"].endswith(("/auth/login", "/wiki/rebuild-links")))
        self.assertFalse(any("/learning/" in request["path"] for request in server.requests))
        reports = self.stdout.getvalue() + self.stderr.getvalue()
        reports += "".join(path.read_text() for path in (self.root / "evidence").glob("*.json"))
        for user in api.users.values():
            for secret in (MODEL["api_key"], user["password"], user["token"]):
                self.assertNotIn(secret, reports)
        self.assertNotIn(MODEL["api_key"], (self.root / "seed-accounts.json").read_text())
        for user in state["users"].values():
            self.assertNotIn("token", user)
        for path in self.root.rglob("*"):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700 if path.is_dir() else 0o600)

    def test_interrupted_registration_resumes_using_saved_credentials(self):
        api = FixtureAPI()
        api.interrupt_registration = True
        server = self.serve(api)
        with mock.patch.dict(os.environ, MODEL_ENV, clear=True):
            self.assert_rejected("unexpected_http_status_503", self.run_seed, "seed",
                                 "--consent-create-fixtures", "--api-base", server.base)
            partial = self.read_json(self.root / "seed-accounts.json")
            first = partial["users"]["learner_a"]
            self.assertNotIn("user_id", first)
            self.assertEqual(len(api.users), 1)
            self.run_seed("seed", "--consent-create-fixtures")
        saved = self.read_json(self.root / "seed-accounts.json")
        for key in ("email", "password", "knowledge_base_id"):
            self.assertEqual(saved["users"]["learner_a"][key], first[key])
        registrations = Counter(request["body"]["email"] for request in server.requests
                                if request["path"] == "/api/v1/auth/register")
        self.assertEqual(registrations, Counter({user["email"]: 1 for user in saved["users"].values()}))
        self.assertEqual(sum(len(user["documents"]) for user in api.users.values()), 6)

    def test_opt_in_is_separate_explicit_and_scoped_to_selected_user(self):
        api, server = self.seed_mock()
        state = self.read_json(self.root / "seed-accounts.json")
        boundary = len(server.requests)
        self.assertEqual(dict(os.environ), {})
        with mock.patch.object(seed_script, "model_config", side_effect=AssertionError("no model needed")):
            self.run_seed("opt-in", "--user", "learner_b")
        self.assertFalse(api.users[state["users"]["learner_a"]["email"]]["enabled"])
        self.assertTrue(api.users[state["users"]["learner_b"]["email"]]["enabled"])
        self.assertEqual([(r["method"], r["path"]) for r in server.requests[boundary:]],
                         [("POST", "/api/v1/auth/login"), ("PUT", "/api/v1/learning/settings")])

    def test_inspect_works_without_model_env_or_environment_credentials(self):
        api, server = self.seed_mock()
        self.assertFalse((self.root / "model.env").exists())
        self.assertEqual(dict(os.environ), {})
        boundary = len(server.requests)
        with mock.patch.object(helper, "model_config", side_effect=AssertionError("no model needed")):
            self.assertEqual(self.run_api("inspect"), 0)
        evidence_files = list((self.root / "evidence").glob("api-acceptance-*.json"))
        self.assertEqual(len(evidence_files), 1)
        evidence = self.read_json(evidence_files[0])
        self.assertEqual(evidence["status"], "pass")
        self.assertEqual(evidence["command"], "inspect")
        self.assertEqual(evidence["counts"], {"observed": 6, "pass": 2})
        for request in server.requests[boundary:]:
            self.assertTrue(request["method"] == "GET" or request["path"] == "/api/v1/auth/login")
        reports = evidence_files[0].read_text() + self.stdout.getvalue() + self.stderr.getvalue()
        for user in api.users.values():
            self.assertFalse(user["enabled"])
            for secret in (MODEL["api_key"], user["password"], user["token"]):
                self.assertNotIn(secret, reports)

    def test_stale_provenance_requires_refresh_then_replaces_page(self):
        api, server = self.seed_mock()
        saved = self.read_json(self.root / "seed-accounts.json")["users"]["learner_a"]
        user = api.users[saved["email"]]
        fixture = seed_script.FIXTURES[0]
        key = (saved["knowledge_base_id"], fixture["slug"])
        old = user["pages"][key]
        old_id = old["id"]
        old["chunk_refs"] = [new_id()]
        boundary = len(server.requests)
        with mock.patch.dict(os.environ, MODEL_ENV, clear=True):
            self.assert_rejected("fixture_provenance_changed_use_refresh_pages",
                                 self.run_seed, "seed", "--consent-create-fixtures")
            self.assertEqual(user["pages"][key]["id"], old_id)
            self.assertFalse(any(r["method"] in ("POST", "PUT", "DELETE") and "/wiki/pages" in r["path"]
                                 for r in server.requests[boundary:]))
            self.run_seed("seed", "--consent-create-fixtures", "--refresh-pages")
        self.assertNotEqual(user["pages"][key]["id"], old_id)
        mutations = [r["method"] for r in server.requests[boundary:]
                     if "/wiki/pages" in r["path"] and r["method"] != "GET"]
        self.assertEqual(mutations, ["DELETE", "POST"])
        self.assertEqual(sum(len(u["documents"]) for u in api.users.values()), 6)

    def test_edited_page_requires_refresh_then_uses_versioned_update(self):
        api, server = self.seed_mock()
        saved = self.read_json(self.root / "seed-accounts.json")["users"]["learner_a"]
        fixture = seed_script.FIXTURES[0]
        page = api.users[saved["email"]]["pages"][(saved["knowledge_base_id"], fixture["slug"])]
        page_id, version = page["id"], page["version"]
        page["content"] = "An intentional local edit."
        boundary = len(server.requests)
        with mock.patch.dict(os.environ, MODEL_ENV, clear=True):
            self.assert_rejected("fixture_page_edited_use_refresh_pages",
                                 self.run_seed, "seed", "--consent-create-fixtures")
            self.assertEqual(page["content"], "An intentional local edit.")
            self.run_seed("seed", "--consent-create-fixtures", "--refresh-pages")
        self.assertEqual(page["id"], page_id)
        self.assertEqual(page["version"], version + 1)
        mutations = [r for r in server.requests[boundary:]
                     if "/wiki/pages" in r["path"] and r["method"] != "GET"]
        self.assertEqual([r["method"] for r in mutations], ["PUT"])
        self.assertEqual(mutations[0]["body"]["version"], version)


class ChunkReadinessTests(OfflineCase):
    def test_only_nonempty_ready_and_strictly_enabled_chunks_are_cited(self):
        ready = {"id": new_id(), "content": "Source evidence.", "index_status": "ready", "is_enabled": True}
        chunks = [ready, {**ready, "id": new_id(), "content": ""},
                  {**ready, "id": new_id(), "index_status": "pending"},
                  {**ready, "id": new_id(), "is_enabled": False},
                  {**ready, "id": new_id(), "is_enabled": 1},
                  {**ready, "id": new_id(), "is_enabled": "true"},
                  {"id": new_id(), "content": "Legacy status is insufficient.", "status": "ready"}]
        client = mock.Mock(spec=helper.Client)
        client.call.side_effect = [{"data": {"parse_status": "completed"}}, {"data": chunks}]
        doc_id = new_id()
        self.assertEqual(seed_script.wait_chunks(client, doc_id, 10), [ready["id"]])
        self.assertEqual(client.call.call_args_list, [
            mock.call("GET", "/knowledge/" + doc_id),
            mock.call("GET", f"/chunks/{doc_id}?page=1&page_size=100")])

    def test_completed_without_usable_chunks_is_not_ready(self):
        ready = {"id": new_id(), "content": "Source evidence.", "index_status": "ready", "is_enabled": True}
        for chunks in ([], [{**ready, "index_status": "pending"}], [{**ready, "is_enabled": False}],
                       [{**ready, "is_enabled": 1}], [{**ready, "content": ""}]):
            with self.subTest(chunks=chunks):
                client = mock.Mock(spec=helper.Client)
                client.call.side_effect = [{"data": {"parse_status": "completed"}}, {"data": chunks}]
                self.assert_rejected("completed_source_has_no_enabled_chunks",
                                     seed_script.wait_chunks, client, new_id(), 10)

    def test_processing_waits_until_completion_before_reading_chunks(self):
        client = mock.Mock(spec=helper.Client)
        chunk = {"id": new_id(), "content": "Evidence.", "index_status": "ready", "is_enabled": True}
        client.call.side_effect = [{"data": {"parse_status": "processing"}},
                                   {"data": {"parse_status": "completed"}}, {"data": [chunk]}]
        doc_id = new_id()
        with mock.patch.object(seed_script, "time") as clock:
            clock.monotonic.side_effect = [0, 0, 2]
            self.assertEqual(seed_script.wait_chunks(client, doc_id, 10), [chunk["id"]])
            clock.sleep.assert_called_once_with(2)
        self.assertEqual(client.call.call_args_list, [
            mock.call("GET", "/knowledge/" + doc_id), mock.call("GET", "/knowledge/" + doc_id),
            mock.call("GET", f"/chunks/{doc_id}?page=1&page_size=100")])

    def test_failed_and_timed_out_sources_never_read_chunks(self):
        client = mock.Mock(spec=helper.Client)
        client.call.return_value = {"data": {"parse_status": "failed"}}
        self.assert_rejected("source_ingestion_failed_use_reparse", seed_script.wait_chunks, client, new_id(), 1)
        self.assertEqual(client.call.call_count, 1)
        client.reset_mock()
        client.call.return_value = {"data": {"parse_status": "processing"}}
        with mock.patch.object(seed_script, "time") as clock:
            clock.monotonic.side_effect = [0, 0, 2]
            self.assert_rejected("source_completion_timeout_check_backend_then_rerun_seed",
                                 seed_script.wait_chunks, client, new_id(), 1)
        self.assertEqual(client.call.call_count, 1)


class ClientTests(OfflineCase):
    def test_api_success_requires_envelope_but_wiki_accepts_bare_objects(self):
        def dispatch(request):
            if "/wiki/" in request["path"]:
                return 200, {"id": "synthetic-page", "chunk_refs": []}, {}
            if request["path"].endswith("/models"):
                return envelope([{"name": MODEL["name"]}])
            return 200, {"data": {"enabled": False}}, {}

        server = self.serve(dispatch)
        client = helper.Client(server.base)
        response = client.call("GET", "/models")
        self.assertEqual(response["data"], [{"name": MODEL["name"]}])
        self.assertTrue(response["no_store"])
        self.assertEqual(client.call("GET", "/knowledgebase/test/wiki/pages/concept/test")["data"]["id"],
                         "synthetic-page")
        self.assert_rejected("http_success_envelope", client.call, "GET", "/learning/settings")

    def test_success_must_be_boolean_true_and_error_payloads_are_redacted(self):
        replies = iter([(200, {"success": value, "data": MODEL["api_key"]}, {})
                        for value in (False, 1, "true")] + [
            (403, {"error": {"code": "learning_forbidden", "message": MODEL["api_key"]}},
             {"Cache-Control": "no-store"})])
        server = self.serve(lambda _request: next(replies))
        client = helper.Client(server.base)
        for _ in range(3):
            self.assert_rejected("http_success_envelope", client.call, "GET", "/learning/settings")
        response = client.call("GET", "/learning/settings", expected=(403,))
        self.assertEqual(response["error_code"], "learning_forbidden")
        self.assertEqual(response["status"], 403)
        self.assertTrue(response["no_store"])

    def test_invalid_response_and_transport_errors_do_not_expose_secrets(self):
        server = self.serve(lambda _request: (200, MODEL["api_key"].encode(), {}))
        client = helper.Client(server.base, "synthetic-bearer-secret")
        self.assert_rejected("invalid_json_redacted", client.call, "GET", "/models")
        with mock.patch.object(client.opener, "open", side_effect=urllib.error.URLError(MODEL["api_key"])):
            self.assert_rejected("http_transport_failure_redacted", client.call, "GET", "/models")

    def test_relative_api_paths_cannot_replace_the_origin(self):
        client = helper.Client()
        for path in ("http://example.invalid", "//example.invalid/path", "models", "/models#secret",
                     "/http://example.invalid"):
            with self.subTest(path=path):
                self.assert_rejected("relative_api_path_only", client.request, "GET", path)

    def test_both_clients_reject_redirects_without_forwarding_credentials(self):
        target = self.serve(lambda _request: envelope({"received": True}))

        def redirect(request):
            code = int(request["path"].rsplit("/", 1)[1])
            return code, {}, {"Location": target.base + "/api/v1/capture"}

        source = self.serve(redirect)
        token = "synthetic-redirect-bearer-secret"
        body = {"email": "synthetic@example.invalid", "password": "synthetic-password"}
        with mock.patch.object(api_script, "API", source.base), mock.patch.dict(
                os.environ, {"HTTP_PROXY": target.base, "http_proxy": target.base}):
            clients = (helper.Client(source.base, token), api_script.Client(token))
            for client in clients:
                for code in (301, 302, 303, 307, 308):
                    for method in ("GET", "POST"):
                        with self.subTest(client=type(client).__module__, code=code, method=method):
                            self.assert_rejected("unexpected_http_status_" + str(code), client.call,
                                                 method, "/redirect/" + str(code),
                                                 body if method == "POST" else None)
        self.assertEqual(target.requests, [])
        self.assertEqual(len(source.requests), 20)
        for request in source.requests:
            self.assertEqual(request["headers"]["Authorization"], "Bearer " + token)
            self.assertEqual(request["body"], body if request["method"] == "POST" else None)


class RecoveryAndCommandTests(OfflineCase):
    def test_nonseed_commands_require_explicit_user_and_never_create_state(self):
        for command in ("opt-in", "configure-agent", "reparse"):
            with self.subTest(command=command), mock.patch.object(helper.Client, "call") as call:
                self.assert_rejected("explicit_fixture_user_required", self.run_seed, command)
                self.assert_rejected("fixtures_missing_run_seed_first", self.run_seed,
                                     command, "--user", "learner_a")
                call.assert_not_called()
                self.assertFalse(self.root.exists())

    def test_seed_validates_saved_distinct_users_bases_and_tenant_types_before_network(self):
        original, _manifest = self.finished_state()
        cases = [("user_id", original["users"]["learner_a"]["user_id"], "distinct_seed_users"),
                 ("knowledge_base_id", original["users"]["learner_a"]["knowledge_base_id"],
                  "distinct_seed_knowledge_bases")]
        cases += [("tenant_id", value, "invalid_seed_tenant") for value in (True, "2", 0, -1, None)]
        for key, value, reason in cases:
            with self.subTest(field=key, kind=type(value).__name__):
                state = copy.deepcopy(original)
                state["users"]["learner_b"][key] = value
                helper.save_json(self.root / "seed-accounts.json", state)
                with mock.patch.object(helper.Client, "call") as call:
                    self.assert_rejected(reason, self.run_seed, "seed", "--consent-create-fixtures")
                call.assert_not_called()

    def test_existing_lock_prevents_model_reads_and_remote_mutations(self):
        self.create_root()
        with helper.fixture_lock(self.root), mock.patch.object(seed_script, "model_config") as config:
            with mock.patch.object(helper.Client, "call") as call:
                self.assert_rejected("fixtures_in_use", self.run_seed, "seed", "--consent-create-fixtures")
            call.assert_not_called()
            config.assert_not_called()
        self.assertFalse((self.root / "seed-accounts.json").exists())

    def test_login_identity_drift_never_mutates_or_overwrites_saved_identity(self):
        api, server = self.seed_mock()
        before = self.read_json(self.root / "seed-accounts.json")
        for field, value in (("id", new_id()), ("email", "foreign@example.invalid"),
                             ("username", "foreign"), ("tenant_id", 999)):
            def dispatch(request):
                status, reply, headers = api(request)
                if request["path"] == "/api/v1/auth/login":
                    reply = copy.deepcopy(reply)
                    reply["user"][field] = value
                return status, reply, headers
            server.dispatch = dispatch
            boundary = len(server.requests)
            with self.subTest(field=field):
                self.assert_rejected("authenticated_seed_identity", self.run_seed,
                                     "opt-in", "--user", "learner_a")
                self.assertEqual(self.read_json(self.root / "seed-accounts.json"), before)
                self.assertEqual([r["path"] for r in server.requests[boundary:]], ["/api/v1/auth/login"])

    def test_shared_authenticated_tenant_is_rejected_before_model_or_knowledge_creation(self):
        api = FixtureAPI()

        def dispatch(request):
            status, reply, headers = api(request)
            if request["path"] == "/api/v1/auth/login" and reply.get("token", "").endswith("-2"):
                reply["user"]["tenant_id"] = 1
                reply["active_tenant"]["id"] = 1
            return status, reply, headers

        server = self.serve(dispatch)
        with mock.patch.dict(os.environ, MODEL_ENV):
            self.assert_rejected("distinct_seed_tenants", self.run_seed, "seed",
                                 "--consent-create-fixtures", "--api-base", server.base)
        self.assertTrue(all("/auth/" in r["path"] for r in server.requests))
        self.assertEqual(len(api.users), 2)
        self.assertFalse((self.root / "evidence/seed.json").exists())

    def test_lost_create_responses_recover_without_duplicate_models_bases_documents_or_pages(self):
        base = self.root
        for index, suffix in enumerate(("/models", "/knowledge-bases", "/knowledge/manual", "/wiki/pages")):
            with self.subTest(endpoint=suffix):
                self.root = base / str(index)
                api = FixtureAPI()
                interrupted = False

                def dispatch(request):
                    nonlocal interrupted
                    response = api(request)
                    if not interrupted and request["method"] == "POST" and request["path"].endswith(suffix):
                        interrupted = True
                        return 503, b"synthetic error body suppressed", {}
                    return response

                server = self.serve(dispatch)
                with mock.patch.dict(os.environ, MODEL_ENV):
                    self.assert_rejected("unexpected_http_status_503", self.run_seed, "seed",
                                         "--consent-create-fixtures", "--api-base", server.base)
                    partial = self.read_json(self.root / "seed-accounts.json")
                    self.assertFalse((self.root / "evidence/seed.json").exists())
                    self.assertEqual(self.run_seed("seed", "--consent-create-fixtures"), 0)
                complete = self.read_json(self.root / "seed-accounts.json")
                for label in helper.ACTORS:
                    for key in ("email", "password", "knowledge_base_id"):
                        self.assertEqual(partial["users"][label][key], complete["users"][label][key])
                self.assertEqual(len(api.users), 2)
                for user in api.users.values():
                    self.assertEqual([len(user[key]) for key in ("models", "bases", "documents", "pages")],
                                     [1, 1, 3, 3])
                self.assertEqual(sum(r["method"] == "POST" and r["path"].endswith(suffix)
                                     for r in server.requests), 6 if index >= 2 else 2)

    def test_ambiguous_recovery_source_is_rejected_without_creation(self):
        api, server = self.seed_mock()
        state = self.read_json(self.root / "seed-accounts.json")
        saved = state["users"]["learner_a"]
        slug = seed_script.FIXTURES[0]["slug"]
        user = api.users[saved["email"]]
        duplicate = {**user["documents"][saved["documents"][slug]], "id": new_id()}
        user["documents"][duplicate["id"]] = duplicate
        del saved["documents"][slug]
        del saved["pages"][slug]
        helper.save_json(self.root / "seed-accounts.json", state)
        boundary = len(server.requests)
        with mock.patch.dict(os.environ, MODEL_ENV):
            self.assert_rejected("ambiguous_fixture_document", self.run_seed,
                                 "seed", "--consent-create-fixtures")
        self.assertTrue(all(r["method"] == "GET" or r["path"] == "/api/v1/auth/login"
                            for r in server.requests[boundary:]))

    def test_configure_agent_is_scoped_idempotent_and_preserves_unrelated_config_and_consent(self):
        api, server = self.seed_mock()
        state = self.read_json(self.root / "seed-accounts.json")
        boundary = len(server.requests)
        with mock.patch.object(seed_script, "model_config", side_effect=AssertionError("no model needed")):
            self.assertEqual(self.run_seed("configure-agent", "--user", "learner_a"), 0)
            self.assertEqual(self.run_seed("configure-agent", "--user", "learner_a"), 0)
        saved = state["users"]["learner_a"]
        user = api.users[saved["email"]]
        self.assertEqual(user["agent"]["config"], {
            "model_id": saved["model_id"], "kb_selection_mode": "selected",
            "knowledge_bases": [saved["knowledge_base_id"]],
            "allowed_tools": ["wiki_read_page", "prepare_learning_quiz"],
        })
        self.assertNotIn("agent", api.users[state["users"]["learner_b"]["email"]])
        self.assertFalse(any(user["enabled"] for user in api.users.values()))
        mutations = [r for r in server.requests[boundary:] if r["method"] == "PUT"]
        self.assertEqual([r["path"] for r in mutations], ["/api/v1/agents/builtin-guided-learning"])
        self.assertFalse(any("/learning/" in r["path"] for r in server.requests[boundary:]))

    def test_configure_agent_rolls_back_after_lost_mutation_response(self):
        api, server = self.seed_mock()
        interrupted = False

        def dispatch(request):
            nonlocal interrupted
            response = api(request)
            if request["method"] == "PUT" and request["path"].endswith("/agents/builtin-guided-learning"):
                if not interrupted:
                    interrupted = True
                    return 503, b"synthetic error after update", {}
            return response

        server.dispatch = dispatch
        boundary = len(server.requests)
        self.assert_rejected("unexpected_http_status_503", self.run_seed,
                             "configure-agent", "--user", "learner_a")
        user = next(iter(api.users.values()))
        self.assertEqual(user["agent"]["config"]["model_id"], "")
        self.assertEqual(user["agent"]["config"]["knowledge_bases"], [])
        self.assertEqual(user["agent"]["config"]["allowed_tools"], ["wiki_read_page", "prepare_learning_quiz"])
        self.assertEqual(sum(r["method"] == "PUT" for r in server.requests[boundary:]), 2)
        self.assertFalse((self.root / "evidence/seed-configure-agent.json").exists())

    def test_reparse_is_scoped_to_failed_owned_sources_then_requires_page_refresh(self):
        api, server = self.seed_mock()
        state = self.read_json(self.root / "seed-accounts.json")
        a, b = (state["users"][label] for label in helper.ACTORS)
        slug = seed_script.FIXTURES[0]["slug"]
        for saved in (a, b):
            api.users[saved["email"]]["documents"][saved["documents"][slug]]["parse_status"] = "failed"
        boundary = len(server.requests)
        with mock.patch.object(seed_script, "model_config", side_effect=AssertionError("no model needed")):
            self.assertEqual(self.run_seed("reparse", "--user", "learner_b"), 0)
        mutations = [r["path"] for r in server.requests[boundary:]
                     if r["method"] == "POST" and r["path"] != "/api/v1/auth/login"]
        self.assertEqual(mutations, ["/api/v1/knowledge/" + b["documents"][slug] + "/reparse"])
        self.assertEqual(api.users[a["email"]]["documents"][a["documents"][slug]]["parse_status"], "failed")
        self.assertEqual(self.read_json(self.root / "seed-accounts.json"), state)
        self.run_seed("reparse", "--user", "all")
        with mock.patch.dict(os.environ, MODEL_ENV):
            self.assert_rejected("fixture_provenance_changed_use_refresh_pages", self.run_seed,
                                 "seed", "--consent-create-fixtures")
            self.assertEqual(self.run_seed("seed", "--consent-create-fixtures", "--refresh-pages"), 0)
        refreshed = self.read_json(self.root / "seed-accounts.json")
        for label in helper.ACTORS:
            self.assertNotEqual(refreshed["users"][label]["pages"][slug], state["users"][label]["pages"][slug])
        self.assertFalse(any(user["enabled"] for user in api.users.values()))

    def test_edited_or_foreign_document_is_never_reparsed(self):
        api, server = self.seed_mock()
        user = next(iter(api.users.values()))
        document = next(iter(user["documents"].values()))
        original = copy.deepcopy(document)
        for field, value, reason in (
                ("knowledge_base_id", new_id(), "source_document_scope"),
                ("metadata", {"content": "An intentional edit.", "status": "publish"}, "fixture_source_edited")):
            with self.subTest(field=field):
                document.update({**original, field: value, "parse_status": "failed"})
                boundary = len(server.requests)
                self.assert_rejected(reason, self.run_seed, "reparse", "--user", "learner_a")
                self.assertFalse(any(r["path"].endswith("/reparse") for r in server.requests[boundary:]))

    def test_graph_evidence_excludes_generated_titles_and_extra_nodes(self):
        api, server = self.seed_mock()
        sentinel = "synthetic-generated-model-output-must-not-be-recorded"

        def dispatch(request):
            status, reply, headers = api(request)
            if request["path"].endswith("/wiki/graph"):
                for node in reply["nodes"]:
                    node["title"] = sentinel
                reply["nodes"].append({"slug": sentinel, "title": sentinel})
            return status, reply, headers

        server.dispatch = dispatch
        with mock.patch.dict(os.environ, MODEL_ENV):
            self.run_seed("seed", "--consent-create-fixtures")
        output = self.stdout.getvalue() + self.stderr.getvalue()
        output += "".join(path.read_text() for path in (self.root / "evidence").glob("*.json"))
        self.assertNotIn(sentinel, output)

    def test_wiki_delete_accepts_only_explicit_empty_204_without_relaxing_other_envelopes(self):
        server = self.serve(lambda _request: (204, b"", {}))
        client = helper.Client(server.base)
        response = client.call("DELETE", "/knowledgebase/test/wiki/pages/concept/test", expected=(204,))
        self.assertEqual((response["status"], response["data"]), (204, {}))
        self.assert_rejected("unexpected_http_status_204", client.call,
                             "DELETE", "/knowledgebase/test/wiki/pages/concept/test")
        self.assert_rejected("http_success_envelope", client.call, "GET", "/models", expected=(204,))

    def test_atomic_replace_failure_preserves_previous_state_and_cleans_temporary_file(self):
        self.create_root()
        path = self.root / "seed-accounts.json"
        helper.save_json(path, {"version": 1})
        with mock.patch.object(helper.os, "replace", side_effect=OSError("synthetic failure")):
            with self.assertRaises(OSError):
                helper.save_json(path, {"version": 2})
        self.assertEqual(self.read_json(path), {"version": 1})
        self.assertEqual(list(self.root.glob(".seed-accounts.json-*")), [])

    def test_invalid_model_ports_and_brackets_are_redacted(self):
        for url in ("https://[invalid", "https://model.example.invalid:bad",
                    "https://model.example.invalid:0", "https://model.example.invalid:65536"):
            with self.subTest(url=url), mock.patch.dict(
                    os.environ, {**MODEL_ENV, "MODEL_BASE_URL": url}):
                self.assert_rejected("model_requires_https_without_url_credentials", helper.model_config)


if __name__ == "__main__":
    unittest.main()
