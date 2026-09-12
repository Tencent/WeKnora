"""Fixed remote helper. No host execution or request-configurable root."""

import base64
import contextlib
import ctypes
import datetime
import errno
import hashlib
import json
import os
import re
import resource
import signal
import stat
import sys
import unicodedata

ROOT = "/workspace/output"
MAX_FILE = 8 << 20
MAX_ENTRIES = 500
MAX_REQUEST = 96 << 10
MAX_RESPONSE = ((MAX_FILE + 2) // 3 * 4) + (64 << 10)
CHUNK = 64 << 10
STAGING_PREFIX = ".weknora-workbench-"
DIR_FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
PIN_FLAGS = os.O_PATH | os.O_NOFOLLOW | os.O_CLOEXEC


class Failure(Exception):
    def __init__(self, code):
        self.code = code


def validate_path(path, allow_root=False):
    if not isinstance(path, str):
        raise Failure("path")
    if path == "" and allow_root:
        return []
    if (not path or len(path.encode("utf-8")) > 4096 or "\\" in path
            or ":" in path or any(unicodedata.category(c) == "Cc" for c in path)):
        raise Failure("path")
    parts = path.split("/")
    if len(parts) > 128 or any(
            p in ("", ".", "..") or len(p.encode("utf-8")) > 255
            or p.startswith(STAGING_PREFIX) for p in parts):
        raise Failure("path")
    return parts


@contextlib.contextmanager
def owned(fd):
    try:
        yield fd
    finally:
        os.close(fd)


def open_absolute_directory(path):
    # Pin / and each root component, not just the final output directory.
    fd = os.open("/", DIR_FLAGS)
    try:
        for name in path.split("/")[1:]:
            if not name:
                raise Failure("path")
            child = os.open(name, DIR_FLAGS, dir_fd=fd)
            os.close(fd)
            fd = child
        return fd
    except BaseException:
        os.close(fd)
        raise


def walk_directory(root, parts):
    fd = os.dup(root)
    try:
        for name in parts:
            child = os.open(name, DIR_FLAGS, dir_fd=fd)
            os.close(fd)
            fd = child
        return fd
    except BaseException:
        os.close(fd)
        raise


def checked_stat(fd):
    info = os.fstat(fd)
    if stat.S_ISREG(info.st_mode):
        if info.st_nlink != 1:
            raise Failure("path")
    elif not stat.S_ISDIR(info.st_mode):
        raise Failure("path")
    return info


def same_inode(a, b):
    return (a.st_dev, a.st_ino) == (b.st_dev, b.st_ino)


def verify_name(parent, name, fd):
    info = checked_stat(fd)
    with owned(os.open(name, PIN_FLAGS, dir_fd=parent)) as current:
        if not same_inode(info, checked_stat(current)):
            raise Failure("conflict")
    return info


def reopen_regular(pin, flags):
    info = checked_stat(pin)
    if not stat.S_ISREG(info.st_mode):
        raise Failure("path")
    # O_PATH never opens a device/FIFO for I/O. Only after classification do
    # we reopen the pinned regular inode using the kernel's own fd magic link.
    # No user-controlled pathname is resolved a second time, even on a race.
    with owned(open_absolute_directory("/proc/%d/fd" % os.getpid())) as proc:
        fd = os.open(str(pin), flags | os.O_CLOEXEC | os.O_NONBLOCK, dir_fd=proc)
    try:
        if not same_inode(info, checked_stat(fd)):
            raise Failure("conflict")
        return fd
    except BaseException:
        os.close(fd)
        raise


def ensure_absent(parent, name):
    try:
        fd = os.open(name, PIN_FLAGS, dir_fd=parent)
    except FileNotFoundError:
        return
    with owned(fd):
        checked_stat(fd)
    raise Failure("conflict")


def read_regular(pin):
    before = checked_stat(pin)
    if not stat.S_ISREG(before.st_mode):
        raise Failure("path")
    if before.st_size > MAX_FILE:
        raise Failure("too_large")
    with owned(reopen_regular(pin, os.O_RDONLY)) as fd:
        data = bytearray()
        while len(data) <= MAX_FILE:
            part = os.read(fd, min(CHUNK, MAX_FILE + 1 - len(data)))
            if not part:
                break
            data.extend(part)
        after = checked_stat(fd)
        if len(data) > MAX_FILE or after.st_size > MAX_FILE:
            raise Failure("too_large")
        if (before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (
                after.st_size, after.st_mtime_ns, after.st_ctime_ns):
            raise Failure("conflict")
        return data


def write_all(fd, data):
    view = memoryview(data)
    for offset in range(0, len(view), CHUNK):
        chunk = view[offset:offset + CHUNK]
        while chunk:
            checked_stat(fd)
            count = os.write(fd, chunk)
            if count <= 0:
                raise Failure("unavailable")
            chunk = chunk[count:]
    checked_stat(fd)


def random_staging_name():
    return STAGING_PREFIX + os.urandom(16).hex()


def open_random_staging(parent):
    for _attempt in range(16):
        name = random_staging_name()
        try:
            fd = os.open(name, os.O_RDWR | os.O_CREAT | os.O_EXCL
                         | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=parent)
            return name, fd
        except FileExistsError:
            pass
    raise Failure("unavailable")


def unlink_if_owned(parent, name, pin):
    expected = os.fstat(pin)
    try:
        current = os.open(name, PIN_FLAGS, dir_fd=parent)
    except FileNotFoundError:
        return
    with owned(current):
        actual = os.fstat(current)
        if (stat.S_ISREG(actual.st_mode) and
                (expected.st_dev, expected.st_ino) == (actual.st_dev, actual.st_ino)):
            os.unlink(name, dir_fd=parent)


def verify_content(pin, expected_size, expected_digest):
    if (type(expected_size) is not int or expected_size < 0 or expected_size > MAX_FILE
            or not isinstance(expected_digest, str)
            or not re.fullmatch("[0-9a-f]{64}", expected_digest)):
        raise Failure("path")
    before = checked_stat(pin)
    if not stat.S_ISREG(before.st_mode):
        raise Failure("path")
    if before.st_size != expected_size:
        raise Failure("conflict")
    digest = hashlib.sha256()
    total = 0
    with owned(reopen_regular(pin, os.O_RDONLY)) as fd:
        while total <= MAX_FILE:
            part = os.read(fd, min(CHUNK, MAX_FILE + 1 - total))
            if not part:
                break
            digest.update(part)
            total += len(part)
        after = checked_stat(fd)
    if total != expected_size or digest.hexdigest() != expected_digest:
        raise Failure("conflict")
    if (before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (
            after.st_size, after.st_mtime_ns, after.st_ctime_ns):
        raise Failure("conflict")


def publish_staging(parent, staging, pin, name, expected_size, expected_digest):
    published = False
    try:
        ensure_absent(parent, name)
        verify_name(parent, staging, pin)
        verify_content(pin, expected_size, expected_digest)
        rename_noreplace(parent, staging, parent, name)
        published = True
        verify_name(parent, name, pin)
    except BaseException:
        unlink_if_owned(parent, name if published else staging, pin)
        raise


def create_file(parent, name, data):
    if len(data) > MAX_FILE:
        raise Failure("too_large")
    staging, fd = open_random_staging(parent)
    with owned(fd):
        try:
            write_all(fd, data)
            os.fsync(fd)
            publish_staging(parent, staging, fd, name, len(data),
                            hashlib.sha256(data).hexdigest())
        except BaseException:
            unlink_if_owned(parent, staging, fd)
            raise


def create_upload(root, name):
    ensure_absent(root, name)
    fd = os.open(name, os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW
                 | os.O_CLOEXEC, 0o600, dir_fd=root)
    with owned(fd):
        try:
            return verify_name(root, name, fd)
        except BaseException:
            unlink_if_owned(root, name, fd)
            raise


def rename_noreplace(src_parent, src, dst_parent, dst):
    libc = ctypes.CDLL(None, use_errno=True)
    renameat2 = getattr(libc, "renameat2", None)
    if renameat2 is None:
        raise Failure("unavailable")
    renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int,
                         ctypes.c_char_p, ctypes.c_uint]
    renameat2.restype = ctypes.c_int
    if renameat2(src_parent, os.fsencode(src), dst_parent, os.fsencode(dst), 1):
        code = ctypes.get_errno()
        if code in (errno.ENOSYS, errno.EOPNOTSUPP):
            raise Failure("unavailable")
        raise OSError(code, "renameat2")


def move_upload_to_parent(root, upload, pin, parent):
    for _attempt in range(16):
        staging = random_staging_name()
        try:
            rename_noreplace(root, upload, parent, staging)
        except FileExistsError:
            continue
        try:
            verify_name(parent, staging, pin)
            return staging
        except BaseException:
            unlink_if_owned(parent, staging, pin)
            raise
    raise Failure("unavailable")


def list_directory(root, parts, path):
    entries = []
    with owned(walk_directory(root, parts)) as directory:
        with os.scandir(directory) as scan:
            for count, entry in enumerate(scan, 1):
                # Bound traversal too, including hidden staging names.
                if count > MAX_ENTRIES:
                    raise Failure("too_large")
                if entry.name.startswith(STAGING_PREFIX):
                    continue
                entry_path = path + "/" + entry.name if path else entry.name
                validate_path(entry_path)
                with owned(os.open(entry.name, PIN_FLAGS, dir_fd=directory)) as fd:
                    info = checked_stat(fd)
                    modified = datetime.datetime.fromtimestamp(
                        info.st_mtime, datetime.timezone.utc).isoformat()
                    entries.append({"name": entry.name, "path": entry_path,
                                    "type": "dir" if stat.S_ISDIR(info.st_mode) else "file",
                                    "size": info.st_size, "modified_at": modified})
    return sorted(entries, key=lambda item: item["name"])


def decode_content(req):
    encoded = req.get("content", "")
    if not isinstance(encoded, str):
        raise Failure("path")
    if len(encoded) > (MAX_FILE + 2) // 3 * 4:
        raise Failure("too_large")
    data = base64.b64decode(encoded, validate=True)
    if len(data) > MAX_FILE:
        raise Failure("too_large")
    return data


def upload_ref(req):
    ref = req.get("upload")
    if (not isinstance(ref, dict) or set(ref) != {"name", "device", "inode"}
            or not isinstance(ref["name"], str)
            or not re.fullmatch(re.escape(STAGING_PREFIX) + "[0-9a-f]{32}", ref["name"])
            or type(ref["device"]) is not int or ref["device"] < 0
            or type(ref["inode"]) is not int or ref["inode"] < 0):
        raise Failure("path")
    return ref


def check_upload(fd, ref):
    info = checked_stat(fd)
    if (not stat.S_ISREG(info.st_mode) or
            (info.st_dev, info.st_ino) != (ref["device"], ref["inode"])):
        raise Failure("conflict")
    return info


def upload_operation(root, req, data):
    op = req["operation"]
    ref = upload_ref(req)
    name = ref["name"]
    if op == "_upload_begin":
        if ref["device"] or ref["inode"] or data:
            raise Failure("path")
        info = create_upload(root, name)
        return {"ok": True, "upload": {"name": name,
                "device": info.st_dev, "inode": info.st_ino}}
    try:
        pin = os.open(name, PIN_FLAGS, dir_fd=root)
    except FileNotFoundError:
        if op == "_upload_abort":
            return {"ok": True}
        raise
    with owned(pin):
        info = check_upload(pin, ref)
        if op == "_upload_abort":
            verify_name(root, name, pin)
            os.unlink(name, dir_fd=root)
        elif op == "_upload_chunk":
            try:
                offset = req.get("offset", 0)
                if type(offset) is not int or offset < 0:
                    raise Failure("path")
                if not data or len(data) > CHUNK or offset + len(data) > MAX_FILE:
                    raise Failure("too_large")
                if info.st_size != offset:
                    raise Failure("conflict")
                with owned(reopen_regular(pin, os.O_WRONLY | os.O_APPEND)) as fd:
                    if check_upload(fd, ref).st_size != offset:
                        raise Failure("conflict")
                    write_all(fd, data)
                    if check_upload(fd, ref).st_size != offset + len(data):
                        raise Failure("conflict")
                    verify_name(root, name, fd)
            except BaseException:
                unlink_if_owned(root, name, pin)
                raise
        else:
            raise Failure("path")
    return {"ok": True}


def commit_upload(root, parent, name, ref, expected_size, expected_digest):
    with owned(os.open(ref["name"], PIN_FLAGS, dir_fd=root)) as pin:
        staging = ""
        upload_owned = False
        try:
            check_upload(pin, ref)
            upload_owned = True
            with owned(reopen_regular(pin, os.O_RDWR)) as fd:
                os.fsync(fd)
            verify_content(pin, expected_size, expected_digest)
            ensure_absent(parent, name)
            staging = move_upload_to_parent(root, ref["name"], pin, parent)
            publish_staging(parent, staging, pin, name, expected_size, expected_digest)
        except BaseException:
            if staging:
                unlink_if_owned(parent, staging, pin)
            elif upload_owned:
                unlink_if_owned(root, ref["name"], pin)
            raise


def perform(req, root_path):
    if not isinstance(req, dict) or set(req) - {
            "operation", "path", "new_path", "content", "upload", "offset", "size", "sha256"}:
        raise Failure("path")
    op = req.get("operation")
    if op not in ("list", "read", "write", "mkdir", "rename", "remove",
                  "_upload_begin", "_upload_chunk", "_upload_abort"):
        raise Failure("path")
    path = req.get("path", "")
    internal = op.startswith("_upload_")
    parts = validate_path(path, op == "list" or internal)
    new_parts = validate_path(req.get("new_path"), False) if op == "rename" else None
    if op != "rename" and req.get("new_path", "") != "":
        raise Failure("path")
    data = decode_content(req)
    if data and op not in ("write", "_upload_chunk"):
        raise Failure("path")
    if req.get("upload") is not None and op != "write" and not internal:
        raise Failure("path")
    if internal and path != "":
        raise Failure("path")
    result = {"path": path, "entries": []}
    with owned(open_absolute_directory(root_path)) as root:
        if internal:
            return upload_operation(root, req, data)
        if op == "list":
            result["entries"] = list_directory(root, parts, path)
        else:
            with owned(walk_directory(root, parts[:-1])) as parent:
                name = parts[-1]
                if op == "write":
                    if req.get("upload") is not None:
                        if data:
                            raise Failure("path")
                        ref = upload_ref(req)
                        commit_upload(root, parent, name, ref, req.get("size"),
                                      req.get("sha256"))
                    else:
                        create_file(parent, name, data)
                elif op == "mkdir":
                    ensure_absent(parent, name)
                    os.mkdir(name, 0o700, dir_fd=parent)
                    with owned(os.open(name, DIR_FLAGS, dir_fd=parent)):
                        pass
                else:
                    with owned(os.open(name, PIN_FLAGS, dir_fd=parent)) as pin:
                        info = checked_stat(pin)
                        if op == "read":
                            content = read_regular(pin)
                            verify_name(parent, name, pin)
                            result["content"] = base64.b64encode(content).decode("ascii")
                        elif op == "remove":
                            verify_name(parent, name, pin)
                            if stat.S_ISDIR(info.st_mode):
                                os.rmdir(name, dir_fd=parent)
                            else:
                                os.unlink(name, dir_fd=parent)
                        elif op == "rename":
                            with owned(walk_directory(root, new_parts[:-1])) as dest:
                                ensure_absent(dest, new_parts[-1])
                                verify_name(parent, name, pin)
                                rename_noreplace(parent, name, dest, new_parts[-1])
                                verify_name(dest, new_parts[-1], pin)
                                result["path"] = req["new_path"]
    return {"ok": True, "result": result}


def dispatch(req, root_path=ROOT):
    # The root parameter is only for in-process tests, never accepted on the wire.
    try:
        return perform(req, root_path)
    except Failure as exc:
        return {"ok": False, "code": exc.code}
    except OSError as exc:
        code = "unavailable"
        if exc.errno in (errno.ELOOP, errno.ENOTDIR, errno.EISDIR, errno.EINVAL,
                         errno.ENAMETOOLONG):
            code = "path"
        elif exc.errno == errno.ENOENT:
            code = "not_found"
        elif exc.errno in (errno.EEXIST, errno.ENOTEMPTY, errno.EBUSY):
            code = "conflict"
        elif exc.errno in (errno.EFBIG, errno.ENOSPC, errno.EDQUOT):
            code = "too_large"
        return {"ok": False, "code": code}
    except (ValueError, TypeError, UnicodeError, OverflowError):
        return {"ok": False, "code": "path"}


def deadline(_signum, _frame):
    raise Failure("unavailable")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise Failure("path")
        result[key] = value
    return result


def main():
    try:
        os.umask(0o077)
        signal.signal(signal.SIGALRM, deadline)
        signal.alarm(10)
        for kind, limit in ((resource.RLIMIT_AS, 256 << 20),
                            (resource.RLIMIT_CPU, 10), (resource.RLIMIT_FSIZE, MAX_FILE)):
            soft, hard = resource.getrlimit(kind)
            finite = [v for v in (soft, hard, limit) if v != resource.RLIM_INFINITY]
            resource.setrlimit(kind, (min(finite), min(finite)))
        raw = sys.stdin.buffer.read(MAX_REQUEST + 1)
        if len(raw) > MAX_REQUEST:
            raise Failure("too_large")
        req = json.loads(raw, object_pairs_hook=unique_object)
        reply = dispatch(req)
    except Failure as exc:
        reply = {"ok": False, "code": exc.code}
    except (ValueError, UnicodeError):
        reply = {"ok": False, "code": "path"}
    except Exception:
        reply = {"ok": False, "code": "unavailable"}
    try:
        encoded = json.dumps(reply, ensure_ascii=True, separators=(",", ":")).encode("ascii") + b"\n"
        if len(encoded) > MAX_RESPONSE:
            encoded = b'{"ok":false,"code":"too_large"}\n'
        # Keep provider stream frames small, even for an 8 MiB read.
        for offset in range(0, len(encoded), 8192):
            sys.stdout.buffer.write(encoded[offset:offset + 8192])
        sys.stdout.buffer.flush()
    except Exception:
        os._exit(1)


if __name__ == "__main__":
    main()
