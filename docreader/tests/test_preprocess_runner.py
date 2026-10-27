import io
import json
import os
from pathlib import Path
import tempfile
import sys
import threading
import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch

import grpc
import numpy as np
from PIL import Image

from docreader.preprocess import MAX_IMAGE_BYTES, PreprocessLimits, PreprocessRunner
from docreader.preprocess_worker import process_image
from docreader.proto import docreader_pb2 as pb, docreader_pb2_grpc as rpc


class RequestContext:
    def __init__(self, remaining=None):
        self.cancelled = threading.Event()
        self.remaining = remaining

    def is_active(self):
        return not self.cancelled.is_set()

    def time_remaining(self):
        return self.remaining


def wait_for(predicate, timeout=5):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        value = predicate()
        if value:
            return value
        time.sleep(0.01)
    raise AssertionError("Timed out waiting for subprocess state")


def process_gone(pid):
    try:
        os.kill(pid, 0)
        return False
    except ProcessLookupError:
        return True


class RunnerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.pool = ThreadPoolExecutor(max_workers=2)
        self.addCleanup(self.pool.shutdown, wait=True)

    def runner(self, **limits):
        runner = PreprocessRunner(PreprocessLimits(**limits),
                                  worker_module="docreader.tests.preprocess_worker_fixture")
        self.addCleanup(runner.close)
        return runner

    def blocking_command(self, **extra):
        started = Path(self.temp.name) / "started"
        release = Path(self.temp.name) / "release"
        command = json.dumps(dict(started=str(started), release=str(release), **extra)).encode()
        return command, started, release

    def started_pid(self, path):
        return int(wait_for(lambda: path.read_text() if path.exists() else ""))

    def assert_reaped(self, pid):
        wait_for(lambda: process_gone(pid))

    def test_busy_declines_without_waiting_or_loading_a_second_engine(self):
        runner = self.runner()
        command, started, release = self.blocking_command()
        first = self.pool.submit(runner.run, command, None)
        pid = self.started_pid(started)
        before = time.monotonic()
        try:
            response = runner.run(b"{}", None)
            self.assertTrue(response.fallback_needed)
            self.assertEqual(response.status, "busy")
            self.assertLess(time.monotonic() - before, 0.5)
        finally:
            release.touch()
        self.assertEqual(first.result(timeout=5).status, "ok")
        self.assertEqual(int(runner.run(b"{}", None).image), pid, "successful calls reuse one worker")

    def test_timeout_kills_running_native_work_and_releases_capacity(self):
        runner = self.runner(timeout_seconds=1.5)
        command, started, _ = self.blocking_command()
        future = self.pool.submit(runner.run, command, None)
        pid = self.started_pid(started)
        response = future.result(timeout=5)
        self.assertEqual(response.status, "timeout")
        self.assertTrue(response.fallback_needed)
        self.assert_reaped(pid)
        next_response = runner.run(b"{}", None)
        self.assertEqual(next_response.status, "ok")
        self.assertNotEqual(int(next_response.image), pid)

    def test_client_deadline_shortens_server_timeout(self):
        runner = self.runner(timeout_seconds=30)
        command, started, _ = self.blocking_command()
        before = time.monotonic()
        future = self.pool.submit(runner.run, command, RequestContext(remaining=1.5))
        pid = self.started_pid(started)
        self.assertEqual(future.result(timeout=5).status, "timeout")
        self.assertLess(time.monotonic() - before, 5)
        self.assert_reaped(pid)

    def test_timeout_interrupts_a_partial_response(self):
        runner = self.runner(timeout_seconds=1.5)
        command, started, _ = self.blocking_command(partial_reply=True)
        future = self.pool.submit(runner.run, command, None)
        pid = self.started_pid(started)
        self.assertEqual(future.result(timeout=5).status, "timeout")
        self.assert_reaped(pid)

    def test_cancellation_interrupts_blocked_image_transfer(self):
        runner = self.runner()
        started = Path(self.temp.name) / "started"
        context = RequestContext()
        with patch.dict(os.environ, {"PREPROCESS_FIXTURE_STALL_INPUT": str(started)}):
            future = self.pool.submit(runner.run, b"x" * MAX_IMAGE_BYTES, context)
            pid = self.started_pid(started)
            context.cancelled.set()
            self.assertEqual(future.result(timeout=5).status, "cancelled")
        self.assert_reaped(pid)
        self.assertEqual(runner.run(b"{}", None).status, "ok")

    def test_cancelled_request_never_starts_a_worker(self):
        runner = self.runner()
        context = RequestContext()
        context.cancelled.set()
        with patch.object(runner, "_start_worker") as start:
            self.assertEqual(runner.run(b"{}", context).status, "cancelled")
        start.assert_not_called()

    def test_memory_limit_kills_worker_and_allows_next_request(self):
        runner = self.runner(memory_mb=96)
        command, started, _ = self.blocking_command(memory=160 * 1024 * 1024)
        future = self.pool.submit(runner.run, command, None)
        pid = self.started_pid(started)
        response = future.result(timeout=5)
        self.assertEqual(response.status, "memory_limit")
        self.assertTrue(response.fallback_needed)
        self.assert_reaped(pid)
        self.assertEqual(runner.run(b"{}", None).status, "ok")

    def test_worker_crash_is_recovered_on_next_request(self):
        runner = self.runner()
        self.assertTrue(runner.run(b'{"crash":true}', None).fallback_needed)
        self.assertEqual(runner.run(b"{}", None).status, "ok")

    @unittest.skipUnless(sys.platform.startswith("linux"), "RLIMIT_AS is enforced in Linux containers")
    def test_linux_worker_has_hard_address_space_limit(self):
        runner = self.runner(memory_mb=128)
        response = runner.run(b'{"check_address_limit":true}', None)
        self.assertEqual(response.status, "ok")
        self.assertEqual(json.loads(response.image), [128 * 1024 * 1024] * 2)

    def test_close_kills_active_work_and_refuses_new_requests(self):
        runner = self.runner()
        command, started, _ = self.blocking_command()
        future = self.pool.submit(runner.run, command, None)
        pid = self.started_pid(started)
        runner.close()
        self.assert_reaped(pid)
        self.assertTrue(future.result(timeout=5).fallback_needed)
        self.assertEqual(runner.run(b"{}", None).status, "cancelled")

    def test_oversized_message_is_declined_before_spawn(self):
        runner = self.runner()
        with patch.object(runner, "_start_worker") as start:
            response = runner.run(b"x" * (MAX_IMAGE_BYTES + 1), None)
        self.assertEqual(response.status, "image_too_large")
        start.assert_not_called()

    def test_grpc_cancellation_reaps_worker_without_blocking_normal_rpcs(self):
        from docreader.main import DocReaderServicer

        runner = self.runner()
        servicer = DocReaderServicer(preprocess_runner=runner)
        self.addCleanup(servicer.close)
        with ThreadPoolExecutor(max_workers=1) as ordinary_pool:
            server = grpc.server(ordinary_pool)
            rpc.add_DocReaderServicer_to_server(servicer, server)
            port = server.add_insecure_port("127.0.0.1:0")
            server.start()
            try:
                with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                    stub = rpc.DocReaderStub(channel)
                    command, started, _ = self.blocking_command()
                    call = stub.PreprocessImage.future(pb.PreprocessImageRequest(image=command), timeout=15)
                    pid = self.started_pid(started)
                    # With only one ordinary RPC thread, using the same executor
                    # for preprocessing would make this request time out.
                    response = stub.ListEngines(pb.ListEnginesRequest(), timeout=2)
                    self.assertTrue(response.engines)
                    document = stub.Read(pb.ReadRequest(
                        file_content=b"# Ordinary document\n\nStill readable.",
                        file_name="ordinary.md", file_type="md"), timeout=2)
                    self.assertIn("Still readable", document.markdown_content)
                    busy = stub.PreprocessImage(pb.PreprocessImageRequest(image=b"{}"), timeout=2)
                    self.assertEqual(busy.status, "busy")
                    self.assertTrue(call.cancel())
                    self.assert_reaped(pid)
                    wait_for(lambda: not runner._idle.empty())
                    self.assertEqual(stub.PreprocessImage(
                        pb.PreprocessImageRequest(image=b"{}"), timeout=5).status, "ok")
            finally:
                server.stop(0).wait()


class ImageBudgetTest(unittest.TestCase):
    def image(self, size):
        out = io.BytesIO()
        Image.new("RGB", size, "white").save(out, format="PNG")
        return out.getvalue()

    def test_pixel_limit_is_checked_before_native_decode(self):
        with patch("cv2.imdecode") as decode, patch("docreader.parser.image_preprocess.preprocess") as preprocess:
            response = process_image(self.image((20, 20)), PreprocessLimits(max_pixels=100))
        self.assertEqual(response.status, "image_too_large")
        decode.assert_not_called()
        preprocess.assert_not_called()

    def test_crop_keeps_original_resolution_when_under_the_output_cap(self):
        # A 4000x4000 page whose only content is a 500x500 block: detect on a
        # 2000 probe, but crop the original, so the block stays 500x500.
        shapes = []
        quad_probe = np.array([[0, 0], [250, 0], [250, 250], [0, 250]], "float32")

        def inspect(img):
            shapes.append(img.shape[:2])
            return {"status": "ok", "rect": np.zeros((10, 10, 3), "uint8"),
                    "quad_input": quad_probe, "n_boxes": 4, "n_kept": 4,
                    "residual": 0.0, "flip": False}

        with patch("docreader.parser.image_preprocess.preprocess", side_effect=inspect):
            response = process_image(self.image((4000, 4000)),
                                     PreprocessLimits(probe_edge=2000, max_edge=2000))
        self.assertEqual(shapes, [(2000, 2000)])
        self.assertEqual(response.status, "ok")
        with Image.open(io.BytesIO(response.image)) as encoded:
            self.assertEqual(encoded.size, (500, 500))

    def test_output_is_capped_after_cropping(self):
        quad_probe = np.array([[0, 0], [200, 0], [200, 200], [0, 200]], "float32")

        def inspect(img):
            return {"status": "ok", "rect": np.zeros((10, 10, 3), "uint8"),
                    "quad_input": quad_probe, "n_boxes": 4, "n_kept": 4,
                    "residual": 0.0, "flip": False}

        with patch("docreader.parser.image_preprocess.preprocess", side_effect=inspect):
            response = process_image(self.image((400, 400)),
                                     PreprocessLimits(probe_edge=200, max_edge=100))
        self.assertEqual(response.status, "ok")
        with Image.open(io.BytesIO(response.image)) as encoded:
            self.assertEqual(encoded.size, (100, 100))

    def test_small_input_uses_the_crop_directly(self):
        rect = np.full((40, 30, 3), 200, "uint8")

        def inspect(img):
            return {"status": "ok", "rect": rect, "n_boxes": 4, "n_kept": 4,
                    "residual": 0.0, "flip": False}

        with patch("docreader.parser.image_preprocess.preprocess", side_effect=inspect):
            response = process_image(self.image((200, 200)), PreprocessLimits(probe_edge=200))
        self.assertEqual(response.status, "ok")
        with Image.open(io.BytesIO(response.image)) as encoded:
            self.assertEqual(encoded.size, (30, 40))

    def test_bad_image_and_declined_geometry_keep_original(self):
        self.assertTrue(process_image(b"not an image", PreprocessLimits()).fallback_needed)
        with patch("docreader.parser.image_preprocess.preprocess",
                   return_value={"status": "ok", "rect": object(), "residual": 3}):
            response = process_image(self.image((20, 20)), PreprocessLimits())
        self.assertTrue(response.fallback_needed)
        self.assertEqual(response.status, "low_quality")
        self.assertFalse(response.image)


if __name__ == "__main__":
    unittest.main()
