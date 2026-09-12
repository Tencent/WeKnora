"""Shared I/O for disposable guided-learning fixtures (Python standard library)."""

from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import re
import shlex
import stat
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

REPO = Path(__file__).resolve().parents[1]
DEFAULT_API = "http://127.0.0.1:28081"
DEFAULT_FRONTEND = "http://127.0.0.1:25173"
ACTORS = ("learner_a", "learner_b")


class SafeFailure(Exception):
    """Only fixed, nonsecret diagnostics may be passed to this exception."""


def require(condition, name):
    if not condition:
        raise SafeFailure(name)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate_json_key")
        result[key] = value
    return result


def strict_json(raw):
    def reject_constant(_value):
        raise SafeFailure("nonfinite_json")
    try:
        return json.loads(raw, object_pairs_hook=unique_object, parse_constant=reject_constant)
    except (ValueError, UnicodeError):
        raise SafeFailure("invalid_json_redacted") from None


def origin(value, *, api=False):
    """Accept loopback HTTP origins without silently dropping URL components."""
    require(isinstance(value, str) and not re.search(r"[\s\\]", value)
            and re.fullmatch(r"http://(?:127\.0\.0\.1|localhost|\[::1\])(?::[0-9]+)?(?:/[^?#]*)?",
                             value, re.IGNORECASE), "loopback_origin_required")
    try:
        url = urllib.parse.urlsplit(value)
        port = url.port
    except ValueError:
        raise SafeFailure("loopback_origin_required") from None
    paths = ("", "/", "/api/v1", "/api/v1/") if api else ("", "/")
    require(url.scheme == "http" and url.hostname in ("127.0.0.1", "localhost", "::1")
            and not url.username and not url.password and url.path in paths
            and (port is None or 1 <= port <= 65535), "loopback_origin_required")
    host = "[::1]" if url.hostname == "::1" else url.hostname
    return "http://" + host + (f":{port}" if port and port != 80 else "")


def runtime_root(value=None, *, create=False):
    base = REPO / ".runtime"
    requested = value if value is not None else os.environ.get("GL_RUNTIME_DIR", str(base))
    require(str(requested) and not re.search(r"[\x00-\x1f\x7f\\]", str(requested))
            and ".." not in Path(requested).parts, "runtime_must_be_under_checkout_runtime")
    selected = Path(requested)
    # Resolve OS aliases of the checkout prefix, never symlinks inside .runtime.
    if selected.is_absolute() and ".runtime" in selected.parts:
        index = selected.parts.index(".runtime")
        prefix = Path(*selected.parts[:index])
        require(not prefix.is_symlink() and prefix.resolve() == REPO,
                "runtime_must_be_under_checkout_runtime")
        selected = REPO.joinpath(*selected.parts[index:])
    selected = Path(os.path.abspath(REPO / selected if not selected.is_absolute() else selected))
    require(selected == base or base in selected.parents, "runtime_must_be_under_checkout_runtime")
    paths = [base]
    for part in selected.relative_to(base).parts:
        paths.append(paths[-1] / part)
    for path in paths:
        if create:
            path.mkdir(mode=0o700, exist_ok=True)
        try:
            info = path.lstat()
        except FileNotFoundError:
            raise SafeFailure("fixtures_missing_run_seed_first") from None
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid()
                and not (info.st_mode & 0o022), "runtime_directory_not_owned_or_writable")
    return selected


def prepare(root):
    for name in ("evidence", "state"):
        path = root / name
        path.mkdir(mode=0o700, exist_ok=True)
        info = path.lstat()
        require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid()
                and not (info.st_mode & 0o022), "runtime_directory_not_owned_or_writable")


def private_text(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        raise SafeFailure("private_file_missing_run_seed_or_supply_model_config") from None
    with os.fdopen(fd) as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid()
                and stat.S_IMODE(info.st_mode) == 0o600 and info.st_nlink == 1,
                "private_file_permissions")
        require(info.st_size <= 1024 * 1024, "private_file_size")
        return stream.read()


def save_json(path, value):
    if path.exists() or path.is_symlink():
        private_text(path)
    content = json.dumps(value, indent=2, allow_nan=False) + "\n"
    fd, name = tempfile.mkstemp(dir=path.parent, prefix="." + path.name + "-")
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(name):
            os.unlink(name)


@contextmanager
def fixture_lock(root):
    fd = os.open(root / "state/fixtures.lock",
                 os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    with os.fdopen(fd, "r+") as lock:
        info = os.fstat(lock.fileno())
        require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600
                and info.st_uid == os.getuid() and info.st_nlink == 1, "fixture_lock_permissions")
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SafeFailure("fixtures_in_use") from None
        yield


def model_config(path=None):
    raw = dict(os.environ)
    if path is not None:
        raw = {}
        for line in private_text(path).splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            key, sep, value = line.removeprefix("export ").partition("=")
            require(sep and re.fullmatch(r"[A-Z][A-Z0-9_]*", key)
                    and key not in raw, "private_model_syntax")
            try:
                values = shlex.split(value, comments=False)
            except ValueError:
                raise SafeFailure("private_model_syntax") from None
            require(len(values) == 1, "private_model_syntax")
            raw[key] = values[0]
    result = {}
    for name, aliases in {"name": ("MODEL_NAME", "DEEPSEEK_MODEL"),
                          "base_url": ("MODEL_BASE_URL", "DEEPSEEK_BASE_URL"),
                          "api_key": ("MODEL_API_KEY", "DEEPSEEK_API_KEY")}.items():
        values = [raw[key] for key in aliases if raw.get(key)]
        require(values and len(set(values)) == 1, "set_model_name_base_url_and_api_key")
        result[name] = values[0]
    try:
        url = urllib.parse.urlsplit(result["base_url"])
        port = url.port
    except ValueError:
        raise SafeFailure("model_requires_https_without_url_credentials") from None
    require(url.scheme == "https" and url.hostname and not url.username and not url.password
            and (port is None or 1 <= port <= 65535)
            and not url.query and not url.fragment and not re.search(r"[\s\\?#]", result["base_url"]),
            "model_requires_https_without_url_credentials")
    return result


def identifier(value):
    require(isinstance(value, str), "invalid_identifier")
    try:
        require(str(uuid.UUID(value)) == value, "invalid_identifier")
    except ValueError:
        raise SafeFailure("invalid_identifier") from None
    return value


def validate_state(state, api):
    require(isinstance(state, dict) and origin(state.get("api_base"), api=True) == api,
            "seed_belongs_to_another_api")
    require("fixture_version" not in state or state["fixture_version"] == 1,
            "unsupported_fixture_version")
    require(isinstance(state.get("users"), dict) and set(state["users"]) == set(ACTORS),
            "seed_user_scope")
    run_id = state.get("run_id")
    require(isinstance(run_id, str) and re.fullmatch(r"[a-f0-9]{12}", run_id), "seed_run_id")
    tenants, users, bases = [], [], []
    for label, user in state["users"].items():
        require(isinstance(user, dict) and user.get("username") == f"gl_{label}_{run_id}"
                and user.get("email") == f"gl-{label}-{run_id}@example.invalid"
                and isinstance(user.get("password"), str)
                and 8 <= len(user["password"]) <= 32, "disposable_seed_only")
        bases.append(identifier(user.get("knowledge_base_id")))
        for key in ("user_id", "model_id"):
            if user.get(key):
                identifier(user[key])
        if user.get("user_id"):
            users.append(user["user_id"])
        if "tenant_id" in user:
            tenant = user["tenant_id"]
            require(type(tenant) is int and tenant > 0, "invalid_seed_tenant")
            tenants.append(tenant)
    require(len(tenants) == len(set(tenants)), "distinct_seed_tenants")
    require(len(users) == len(set(users)), "distinct_seed_users")
    require(len(bases) == len(set(bases)), "distinct_seed_knowledge_bases")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, base=DEFAULT_API, token=None):
        self.base, self.token = origin(base, api=True), token
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method, path, body=None):
        require(path.startswith("/") and not path.startswith("//") and "://" not in path
                and "#" not in path, "relative_api_path_only")
        headers = {"Accept": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        data = None if body is None else json.dumps(body, allow_nan=False).encode()
        if data is not None:
            headers["Content-Type"] = "application/json"
        return urllib.request.Request(self.base + "/api/v1" + path, data=data,
                                      headers=headers, method=method)

    def call(self, method, path, body=None, expected=(200,), timeout=45):
        started = time.monotonic()
        try:
            response = self.opener.open(self.request(method, path, body), timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except (urllib.error.URLError, TimeoutError, OSError):
            raise SafeFailure("http_transport_failure_redacted") from None
        with response:
            status = response.code
            raw = response.read(8 * 1024 * 1024 + 1)
            require(len(raw) <= 8 * 1024 * 1024, "http_response_size")
            require(status in expected, "unexpected_http_status_" + str(status))
            value = strict_json(raw) if raw else {}
            no_store = "no-store" in response.headers.get("Cache-Control", "")
        require(isinstance(value, dict), "http_response_object")
        if status < 300:
            bare_wiki = path.startswith("/knowledgebase/") and "/wiki/" in path
            require(isinstance(value, dict) and (bare_wiki or value.get("success") is True),
                    "http_success_envelope")
        return {"status": status, "data": value.get("data", value),
                "error_code": value.get("error", {}).get("code")
                if isinstance(value.get("error"), dict) else None,
                "no_store": no_store, "seconds": round(time.monotonic() - started, 3)}
