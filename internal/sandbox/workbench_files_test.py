"""Run with python3 -I workbench_files_test.py; uses only temporary roots."""

import base64
import hashlib
import importlib.util
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "workbench_files", pathlib.Path(__file__).with_name("workbench_files.py"))
helper = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(helper)


class WorkbenchFilesTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="workbench-files-")
        self.addCleanup(self.tmp.cleanup)
        self.base = pathlib.Path(self.tmp.name)
        self.root = self.base / "output"
        self.root.mkdir()
        self.outside = self.base / "outside"
        self.outside.mkdir()
        (self.outside / "secret").write_bytes(b"outside-secret")

    def call(self, operation, path="", content=None, **extra):
        req = {"operation": operation, "path": path, **extra}
        if content is not None:
            req["content"] = base64.b64encode(content).decode("ascii")
        return helper.dispatch(req, str(self.root))

    def ok(self, reply):
        self.assertTrue(reply["ok"], reply)
        return reply.get("result")

    def error(self, reply, code):
        self.assertEqual({"ok": False, "code": code}, reply)

    def test_roundtrip_binary_empty_and_directories(self):
        self.assertEqual([], self.ok(self.call("list"))["entries"])
        self.ok(self.call("mkdir", "dir"))
        data = bytes(range(256)) + b"\x00\xff"
        self.ok(self.call("write", "dir/a", data))
        self.assertEqual(data, base64.b64decode(self.ok(self.call("read", "dir/a"))["content"]))
        entries = self.ok(self.call("list", "dir"))["entries"]
        self.assertEqual(("a", "dir/a", "file", len(data)),
                         tuple(entries[0][k] for k in ("name", "path", "type", "size")))
        self.assertTrue(entries[0]["modified_at"].endswith("+00:00"))
        self.ok(self.call("rename", "dir", new_path="renamed"))
        self.error(self.call("remove", "renamed"), "conflict")
        self.ok(self.call("rename", "renamed/a", new_path="renamed/b"))
        self.ok(self.call("remove", "renamed/b"))
        self.ok(self.call("remove", "renamed"))
        self.ok(self.call("write", "empty", b""))
        self.assertEqual("", self.ok(self.call("read", "empty"))["content"])

    def test_no_overwrites_or_recursive_mkdir(self):
        self.ok(self.call("write", "a", b"keep"))
        self.ok(self.call("write", "b", b"other"))
        self.error(self.call("write", "a", b"replace"), "conflict")
        self.error(self.call("mkdir", "a"), "conflict")
        self.error(self.call("rename", "b", new_path="a"), "conflict")
        self.error(self.call("rename", "a", new_path="a"), "conflict")
        self.error(self.call("mkdir", "missing/child"), "not_found")
        self.error(self.call("read", "missing"), "not_found")
        self.assertEqual(b"keep", (self.root / "a").read_bytes())
        self.ok(self.call("mkdir", "d1"))
        self.ok(self.call("mkdir", "d2"))
        self.error(self.call("rename", "d1", new_path="d2"), "conflict")

    def test_failed_direct_write_removes_staging_and_destination(self):
        original = helper.os.write
        failed = False

        def fail_after_partial_write(fd, data):
            nonlocal failed
            if not failed:
                failed = True
                original(fd, data[:1])
                raise OSError(5, "injected write failure")
            return original(fd, data)

        with mock.patch.object(helper.os, "write", fail_after_partial_write):
            self.error(self.call("write", "broken", b"partial-data"), "unavailable")
        self.assertFalse((self.root / "broken").exists())
        self.assertEqual([], list(self.root.glob(helper.STAGING_PREFIX + "*")))

    def test_failed_upload_chunk_removes_staging(self):
        template = {"name": helper.STAGING_PREFIX + "f" * 32,
                    "device": 0, "inode": 0}
        ref = self.call("_upload_begin", upload=template)["upload"]
        original = helper.os.write

        def fail_write(fd, data):
            original(fd, data[:1])
            raise OSError(5, "injected chunk failure")

        with mock.patch.object(helper.os, "write", fail_write):
            self.error(self.call("_upload_chunk", content=b"chunk", upload=ref),
                       "unavailable")
        self.assertFalse((self.root / ref["name"]).exists())
        self.assertEqual([], list(self.root.glob(helper.STAGING_PREFIX + "*")))

    def test_lexical_validation_and_root_restriction(self):
        for op in ("read", "write", "mkdir", "rename", "remove"):
            self.error(self.call(op), "path")
        paths = ["/abs", "..", "../secret", "a/../b", ".", "a/./b", "a//b",
                 "a/", "\\x", "a\\b", "C:/x", "a\x00b", "a\nb", "a\x7fb",
                 "a\u0085b", "\udcff", "x" * 256, "a/" * 128 + "b",
                 ".weknora-workbench-x", "a/.weknora-workbench-x"]
        for path in paths:
            with self.subTest(path=repr(path)):
                self.error(self.call("list", path), "path")
                self.error(self.call("rename", "a", new_path=path), "path")
        self.error(self.call("unknown"), "path")
        self.error(self.call("list", new_path="a"), "path")
        self.error(self.call("list", content=b"x"), "path")
        self.error(helper.dispatch({"operation": "list", "root": str(self.outside)}), "path")

    def test_names_are_data_not_shell(self):
        for name in ["';$(touch pwned);&.txt", "WEKNORA_STDIN_EOF", "spaced name", "\u4e2d\u6587.txt"]:
            self.ok(self.call("write", name, b"safe"))
            self.assertEqual(b"safe", base64.b64decode(self.ok(self.call("read", name))["content"]))
        self.assertFalse((self.root / "pwned").exists())

    def test_symlinks_hardlinks_and_special_nodes_are_rejected(self):
        os.symlink(self.outside / "secret", self.root / "symlink")
        os.link(self.outside / "secret", self.root / "hardlink")
        os.mkfifo(self.root / "fifo")
        sock = socket.socket(socket.AF_UNIX)
        self.addCleanup(sock.close)
        sock.bind(str(self.root / "socket"))
        for name in ("symlink", "hardlink", "fifo", "socket"):
            for op in ("read", "write", "mkdir", "rename", "remove"):
                with self.subTest(name=name, operation=op):
                    extra = {"new_path": "dest"} if op == "rename" else {}
                    self.error(self.call(op, name, **extra), "path")
        self.error(self.call("list"), "path")
        self.assertEqual(b"outside-secret", (self.outside / "secret").read_bytes())

    def test_device_is_never_opened_for_io(self):
        original = helper.os.open
        # Use a real device inode without requiring CAP_MKNOD in CI. Inject
        # only its O_PATH lookup; any attempt to open it for I/O fails the test.
        with helper.owned(os.open("/dev/null", helper.PIN_FLAGS)) as device:
            def device_lookup(name, flags, *args, **kwargs):
                if name == "device":
                    self.assertTrue(flags & os.O_PATH)
                    return os.dup(device)
                return original(name, flags, *args, **kwargs)

            with mock.patch.object(helper.os, "open", device_lookup):
                for op in ("read", "write", "mkdir", "rename", "remove"):
                    extra = {"new_path": "dest"} if op == "rename" else {}
                    self.error(self.call(op, "device", **extra), "path")

    def test_root_and_intermediate_symlinks(self):
        os.symlink(self.outside, self.root / "link")
        for op in ("list", "read", "write", "mkdir", "remove", "rename"):
            extra = {"new_path": "b"} if op == "rename" else {}
            self.error(self.call(op, "link/secret", **extra), "path")
        root_link = self.base / "root-link"
        os.symlink(self.outside, root_link)
        self.error(helper.dispatch({"operation": "list"}, str(root_link)), "path")
        (self.outside / "output").mkdir()
        self.error(helper.dispatch({"operation": "list"}, str(root_link / "output")), "path")

    def test_pinned_parent_survives_symlink_swap_for_all_operations(self):
        original = helper.walk_directory
        for op in ("read", "write", "mkdir", "remove", "rename"):
            with self.subTest(operation=op):
                parent = self.root / op
                parent.mkdir()
                (parent / "secret").write_bytes(b"inside")
                swapped = False

                def swap(root, parts):
                    nonlocal swapped
                    fd = original(root, parts)
                    if parts == [op] and not swapped:
                        swapped = True
                        parent.rename(self.root / (op + "-pinned"))
                        os.symlink(self.outside, parent)
                    return fd

                with mock.patch.object(helper, "walk_directory", swap):
                    leaf = "new" if op in ("write", "mkdir") else "secret"
                    extra = {"new_path": op + "-renamed"} if op == "rename" else {}
                    reply = self.call(op, op + "/" + leaf, **extra)
                    result = self.ok(reply)
                    if op == "read":
                        self.assertEqual(b"inside", base64.b64decode(result["content"]))
                self.assertEqual(b"outside-secret", (self.outside / "secret").read_bytes())
                self.assertFalse((self.outside / "new").exists())

    def test_root_descriptor_survives_root_swap(self):
        (self.root / "secret").write_bytes(b"inside")
        original = helper.open_absolute_directory

        def swap(path):
            fd = original(path)
            if path == str(self.root):
                self.root.rename(self.base / "pinned-output")
                os.symlink(self.outside, self.root)
            return fd

        with mock.patch.object(helper, "open_absolute_directory", swap):
            self.assertEqual(b"inside", base64.b64decode(self.ok(self.call("read", "secret"))["content"]))

    def test_leaf_swap_between_pin_and_io(self):
        (self.root / "leaf").write_bytes(b"inside")
        original = helper.reopen_regular

        def swap(pin, flags):
            (self.root / "leaf").rename(self.root / "old-leaf")
            os.symlink(self.outside / "secret", self.root / "leaf")
            return original(pin, flags)

        with mock.patch.object(helper, "reopen_regular", swap):
            # Filesystems with ctime precision high enough detect the pinned
            # inode rename during the read (conflict); others reach the final
            # name verification and reject the replacement symlink (path).
            reply = self.call("read", "leaf")
            self.assertFalse(reply["ok"])
            self.assertIn(reply["code"], ("path", "conflict"))
        self.assertEqual(b"outside-secret", (self.outside / "secret").read_bytes())

    def test_hardlink_added_during_read(self):
        (self.root / "leaf").write_bytes(b"inside")
        original = helper.os.read
        added = False

        def add_link(fd, amount):
            nonlocal added
            if not added:
                os.link(self.root / "leaf", self.outside / "new-link")
                added = True
            return original(fd, amount)

        with mock.patch.object(helper.os, "read", add_link):
            self.error(self.call("read", "leaf"), "path")

    def test_rename_target_race_never_clobbers_file_or_directory(self):
        original = helper.rename_noreplace
        for directory in (False, True):
            src = "dir" if directory else "file"
            if directory:
                (self.root / src).mkdir()
            else:
                (self.root / src).write_bytes(b"source")

            def insert_target(src_parent, source, dst_parent, target):
                os.symlink(self.outside / "secret", self.root / target)
                return original(src_parent, source, dst_parent, target)

            with mock.patch.object(helper, "rename_noreplace", insert_target):
                self.error(self.call("rename", src, new_path=src + "-dest"), "conflict")
            self.assertTrue((self.root / src).exists())
            self.assertTrue((self.root / (src + "-dest")).is_symlink())

    def test_file_and_listing_bounds(self):
        maximum = b"x" * helper.MAX_FILE
        self.ok(self.call("write", "max", maximum))
        self.assertEqual(maximum, base64.b64decode(self.ok(self.call("read", "max"))["content"]))
        self.error(self.call("write", "over", maximum + b"x"), "too_large")
        with (self.root / "huge").open("wb") as stream:
            stream.truncate(1 << 40)
        self.error(self.call("read", "huge"), "too_large")
        self.ok(self.call("mkdir", "many"))
        for i in range(helper.MAX_ENTRIES):
            (self.root / "many" / str(i)).touch()
        self.assertEqual(helper.MAX_ENTRIES, len(self.ok(self.call("list", "many"))["entries"]))
        (self.root / "many" / "over").touch()
        self.error(self.call("list", "many"), "too_large")

    def test_growing_read_is_bounded(self):
        target = self.root / "growing"
        target.write_bytes(b"start")
        original = helper.os.read
        consumed = 0

        def grow(fd, amount):
            nonlocal consumed
            if consumed == 0:
                with target.open("ab") as stream:
                    stream.truncate(1 << 30)
            data = original(fd, amount)
            consumed += len(data)
            return data

        with mock.patch.object(helper.os, "read", grow):
            self.error(self.call("read", "growing"), "too_large")
        self.assertLessEqual(consumed, helper.MAX_FILE + 1)

    def test_upload_identity_digest_and_cleanup(self):
        template = {"name": helper.STAGING_PREFIX + "a" * 32,
                    "device": 0, "inode": 0}
        begin = self.call("_upload_begin", upload=template)
        self.ok(begin)
        ref = begin["upload"]
        self.ok(self.call("_upload_chunk", content=b"chunk", upload=ref))
        self.error(self.call("_upload_chunk", content=b"again", upload=ref), "conflict")
        self.assertFalse((self.root / ref["name"]).exists())
        begin = self.call("_upload_begin", upload=template)
        ref = begin["upload"]
        self.ok(self.call("_upload_chunk", content=b"chunk", upload=ref))
        self.error(self.call("write", "dest", upload=ref, size=5,
                             sha256="0" * 64), "conflict")
        self.assertFalse((self.root / "dest").exists())
        self.assertFalse((self.root / ref["name"]).exists())
        begin = self.call("_upload_begin", upload=template)
        ref = begin["upload"]
        self.ok(self.call("_upload_chunk", content=b"chunk", upload=ref))
        self.ok(self.call("write", "dest", upload=ref, size=5, sha256=hashlib.sha256(b"chunk").hexdigest()))
        self.ok(self.call("_upload_abort", upload=ref))
        self.assertFalse((self.root / ref["name"]).exists())
        self.assertEqual(b"chunk", (self.root / "dest").read_bytes())
        begin = self.call("_upload_begin", upload=template)
        ref = begin["upload"]
        (self.root / ref["name"]).rename(self.root / "old-upload")
        (self.root / ref["name"]).write_bytes(b"replacement")
        self.error(self.call("_upload_abort", upload=ref), "conflict")
        self.assertEqual(b"replacement", (self.root / ref["name"]).read_bytes())

    def test_concurrent_intermediate_swaps_never_read_outside(self):
        live = self.root / "live"
        parked = self.root / "parked"
        live.mkdir()
        (live / "secret").write_bytes(b"inside")
        stop = threading.Event()

        def race():
            while not stop.is_set():
                live.rename(parked)
                os.symlink(self.outside, live)
                live.unlink()
                parked.rename(live)

        thread = threading.Thread(target=race)
        thread.start()
        try:
            for _ in range(150):
                reply = self.call("read", "live/secret")
                if reply["ok"]:
                    self.assertEqual(b"inside", base64.b64decode(reply["result"]["content"]))
                else:
                    self.assertIn(reply["code"], ("path", "not_found", "conflict"))
        finally:
            stop.set()
            thread.join(5)
        self.assertFalse(thread.is_alive())

    def test_failed_traversals_do_not_leak_descriptors(self):
        os.symlink(self.outside, self.root / "link")
        before = len(os.listdir("/proc/self/fd"))
        for _ in range(100):
            self.call("read", "link/secret")
            self.call("read", "missing/secret")
        self.assertEqual(before, len(os.listdir("/proc/self/fd")))

    def test_main_protocol_is_bounded_and_rejects_root_override(self):
        for payload, code in [(b"not json", "path"),
                              (b'{"operation":"list","operation":"read"}', "path"),
                              (b'{"operation":"list","root":"/tmp"}', "path"),
                              (b"x" * (helper.MAX_REQUEST + 1), "too_large")]:
            out = subprocess.run([sys.executable, "-I", "-c", pathlib.Path(SPEC.origin).read_text()],
                                 input=payload, capture_output=True, timeout=15, check=True)
            self.error(json.loads(out.stdout), code)
            self.assertEqual(b"", out.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
