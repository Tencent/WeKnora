"""Bounded, cancellable subprocesses for optional image preprocessing.

The gRPC handler never runs native OCR itself. A busy pool declines immediately,
and an expired/cancelled request kills its worker before returning the slot.
"""

import json
import logging
import os
from pathlib import Path
import queue
import select
import socket
import struct
import subprocess
import sys
import threading
import time
from dataclasses import asdict, dataclass

from docreader.proto.docreader_pb2 import PreprocessImageResponse

logger = logging.getLogger(__name__)
MAX_IMAGE_BYTES = 20 * 1024 * 1024
POLL_SECONDS = 0.05
WORKER_IDLE_SECONDS = 60


@dataclass(frozen=True)
class PreprocessLimits:
    max_workers: int = 1
    timeout_seconds: float = 30
    threads: int = 2
    memory_mb: int = 2048
    max_pixels: int = 16_000_000
    max_edge: int = 2000
    # Detection runs on a copy capped at probe_edge; the crop itself is taken
    # from the original image and only capped at max_edge.
    probe_edge: int = 2000

    def __post_init__(self):
        if any(value <= 0 for value in asdict(self).values()):
            raise ValueError("Preprocessing resource limits must be positive")

    @classmethod
    def from_config(cls, config):
        return cls(**{name: getattr(config, f"preprocess_{name}") for name in asdict(cls())})


class _Stopped(Exception):
    pass


class _Channel:
    """Size-bounded byte frames, with cancellable reads AND writes.

    Readability alone does not guarantee a complete frame. Nonblocking I/O
    keeps supervision running if a child stalls halfway through a message.
    """

    def __init__(self, sock):
        self.socket = sock
        sock.setblocking(False)

    def close(self):
        self.socket.close()

    def poll(self, timeout):
        return bool(select.select([self.socket], [], [], timeout)[0])

    def _transfer(self, buffer, *, writing=False, check=None):
        view = memoryview(buffer)
        while view:
            if check is not None:
                check()
            readers, writers = ([], [self.socket]) if writing else ([self.socket], [])
            readable, writable, _ = select.select(readers, writers, [], POLL_SECONDS)
            if not (readable or writable):
                continue
            try:
                size = self.socket.send(view) if writing else self.socket.recv_into(view)
            except BlockingIOError:
                continue
            if not size:
                raise EOFError("Preprocessing worker closed its channel")
            view = view[size:]
        if check is not None:
            check()

    def send_bytes(self, data, *, check=None):
        if len(data) > MAX_IMAGE_BYTES:
            raise ValueError("Preprocessing message exceeds the byte limit")
        self._transfer(struct.pack("!I", len(data)), writing=True, check=check)
        self._transfer(data, writing=True, check=check)

    def recv_bytes(self, *, check=None):
        header = bytearray(4)
        self._transfer(header, check=check)
        size, = struct.unpack("!I", header)
        if size > MAX_IMAGE_BYTES:
            raise ValueError("Preprocessing message exceeds the byte limit")
        data = bytearray(size)
        self._transfer(data, check=check)
        return bytes(data)


class _Worker:
    def __init__(self, process, connection):
        self.process = process
        self.connection = connection
        self.ready = False
        self.lock = threading.Lock()

    def stop(self):
        with self.lock:
            if self.process.poll() is None:
                self.process.kill()
            self.process.wait(timeout=2)
            self.connection.close()


def _resident_bytes(pid):
    if sys.platform.startswith("linux"):
        with open(f"/proc/{pid}/statm", encoding="ascii") as stat:
            return int(stat.read().split()[1]) * os.sysconf("SC_PAGE_SIZE")
    if sys.platform == "darwin":
        # Development hosts do not have /proc. Linux containers use the cheap
        # statm read above, plus a hard address-space limit in the child.
        result = subprocess.run(
            ["/bin/ps", "-o", "rss=", "-p", str(pid)],
            capture_output=True, text=True, timeout=0.5, check=True,
        )
        return int(result.stdout.strip()) * 1024
    raise OSError("Preprocessing memory supervision requires Linux or macOS")


class PreprocessRunner:
    def __init__(self, limits, *, worker_module="docreader.preprocess_worker"):
        self.limits = limits
        self._worker_module = worker_module
        self._idle = queue.LifoQueue(limits.max_workers)
        for _ in range(limits.max_workers):
            self._idle.put_nowait(None)
        self._closed = threading.Event()
        self._lock = threading.Lock()
        self._workers = set()

    def _start_worker(self):
        parent, child = socket.socketpair()
        env = os.environ.copy()
        # Set before Python imports numerical libraries in the child, without
        # changing the environment of ordinary document parsers.
        for name in ("OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS",
                     "VECLIB_MAXIMUM_THREADS", "NUMEXPR_NUM_THREADS"):
            env[name] = str(self.limits.threads)
        env["DOCREADER_PREPROCESS_THREADS"] = str(self.limits.threads)
        try:
            with self._lock:
                if self._closed.is_set():
                    raise _Stopped("cancelled")
                process = subprocess.Popen(
                    [sys.executable, "-m", self._worker_module, str(child.fileno()),
                     json.dumps(asdict(self.limits)), str(os.getpid())],
                    pass_fds=(child.fileno(),), env=env,
                    cwd=Path(__file__).resolve().parent.parent,
                )
                worker = _Worker(process, _Channel(parent))
                self._workers.add(worker)
                return worker
        except BaseException:
            parent.close()
            raise
        finally:
            child.close()

    def _discard(self, worker):
        if worker is None:
            return True
        try:
            worker.stop()
        except (OSError, subprocess.TimeoutExpired):
            # Do not admit a replacement while an old process may still exist.
            logger.exception("Could not reap preprocessing worker %s", worker.process.pid)
            return False
        with self._lock:
            self._workers.discard(worker)
        return True

    def _check_request(self, context, deadline):
        if self._closed.is_set():
            raise _Stopped("cancelled")
        if time.monotonic() >= deadline:
            raise _Stopped("timeout")
        if context is not None and not context.is_active():
            raise _Stopped("cancelled")

    def _check_worker(self, worker):
        if worker.process.poll() is not None:
            raise _Stopped("worker_failed")
        try:
            rss = _resident_bytes(worker.process.pid)
        except (OSError, ValueError, subprocess.SubprocessError):
            # Fail closed if the worker cannot be supervised. A process that
            # exited between poll() and reading RSS is handled the same way.
            raise _Stopped("worker_failed") from None
        if rss > self.limits.memory_mb * 1024 * 1024:
            raise _Stopped("memory_limit")

    def _supervisor(self, worker, context, deadline):
        def check():
            self._check_request(context, deadline)
            self._check_worker(worker)
        return check

    def run(self, image, context):
        deadline = time.monotonic() + self.limits.timeout_seconds
        if context is not None:
            remaining = context.time_remaining()
            if remaining is not None:
                deadline = min(deadline, time.monotonic() + max(0, remaining))
        worker = None
        reusable = False
        acquired = False
        try:
            self._check_request(context, deadline)
            if len(image) > MAX_IMAGE_BYTES:
                raise _Stopped("image_too_large")
            try:
                worker = self._idle.get_nowait()
            except queue.Empty:
                raise _Stopped("busy") from None
            acquired = True
            # Idle children exit themselves, releasing their model memory.
            if worker is not None and worker.process.poll() is not None:
                if not self._discard(worker):
                    acquired = False
                    raise _Stopped("worker_failed")
                worker = None
            if worker is None:
                worker = self._start_worker()
            check = self._supervisor(worker, context, deadline)
            if not worker.ready:
                if worker.connection.recv_bytes(check=check) != b"ready":
                    raise _Stopped("worker_failed")
                worker.ready = True
            worker.connection.send_bytes(image, check=check)
            response = PreprocessImageResponse.FromString(worker.connection.recv_bytes(check=check))
            reusable = response.status not in ("memory_limit", "failed")
            return response
        except _Stopped as exc:
            logger.info("PreprocessImage: fallback (%s)", exc)
            return PreprocessImageResponse(fallback_needed=True, status=str(exc))
        except Exception:
            logger.exception("PreprocessImage worker failed; keeping the original image")
            return PreprocessImageResponse(fallback_needed=True, status="worker_failed")
        finally:
            if acquired:
                if not reusable or self._closed.is_set():
                    reaped = self._discard(worker)
                    worker = None
                else:
                    reaped = True
                if reaped and not self._closed.is_set():
                    self._idle.put_nowait(worker)

    def close(self):
        self._closed.set()
        with self._lock:
            workers = list(self._workers)
        for worker in workers:
            self._discard(worker)
