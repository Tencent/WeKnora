import threading
import unittest
import weakref
from unittest.mock import patch

from docreader.utils.request_memory import (
    RequestThreadPoolExecutor,
    track_request_size,
)


class RequestMemoryTest(unittest.TestCase):
    def test_large_request_is_reclaimed_after_worker_locals_are_released(self):
        references = []

        class Payload:
            pass

        def rpc_task():
            payload = Payload()
            references.append(weakref.ref(payload))
            track_request_size(64 * 1024 * 1024)
            return "completed"

        def collect():
            self.assertIsNone(references[0]())

        with patch("docreader.utils.request_memory.gc.collect", side_effect=collect) as gc_collect:
            with patch("docreader.utils.request_memory._malloc_trim") as trim:
                with RequestThreadPoolExecutor(max_workers=1) as pool:
                    self.assertEqual(pool.submit(rpc_task).result(), "completed")
                    self.assertEqual(pool.submit(lambda: "small").result(), "small")

        gc_collect.assert_called_once_with()
        trim.assert_called_once_with(0)

    def test_failed_large_request_is_reclaimed(self):
        def rpc_task():
            track_request_size(64 * 1024 * 1024)
            raise ValueError("parse failed")

        with patch("docreader.utils.request_memory.gc.collect") as gc_collect:
            with patch("docreader.utils.request_memory._malloc_trim") as trim:
                with RequestThreadPoolExecutor(max_workers=1) as pool:
                    with self.assertRaisesRegex(ValueError, "parse failed"):
                        pool.submit(rpc_task).result()

        gc_collect.assert_called_once_with()
        trim.assert_called_once_with(0)

    def test_small_requests_do_not_trigger_collection(self):
        def rpc_task():
            track_request_size(1024)
            return 42

        with patch("docreader.utils.request_memory.gc.collect") as gc_collect:
            with patch("docreader.utils.request_memory._malloc_trim") as trim:
                with RequestThreadPoolExecutor(max_workers=1) as pool:
                    self.assertEqual(pool.submit(rpc_task).result(), 42)

        gc_collect.assert_not_called()
        trim.assert_not_called()

    def test_collection_works_without_malloc_trim(self):
        with patch("docreader.utils.request_memory.gc.collect") as gc_collect:
            with patch("docreader.utils.request_memory._malloc_trim", None):
                with RequestThreadPoolExecutor(max_workers=1) as pool:
                    pool.submit(track_request_size, 64 * 1024 * 1024).result()

        gc_collect.assert_called_once_with()

    def test_cleanup_flag_is_local_to_the_large_request_worker(self):
        barrier = threading.Barrier(2)
        collected_on = []

        def rpc_task(size):
            track_request_size(size)
            barrier.wait(timeout=5)
            return threading.get_ident()

        with patch(
            "docreader.utils.request_memory.gc.collect",
            side_effect=lambda: collected_on.append(threading.get_ident()),
        ):
            with patch("docreader.utils.request_memory._malloc_trim") as trim:
                with RequestThreadPoolExecutor(max_workers=2) as pool:
                    large = pool.submit(rpc_task, 64 * 1024 * 1024)
                    small = pool.submit(rpc_task, 1024)
                    large_thread = large.result()
                    self.assertNotEqual(large_thread, small.result())

        self.assertEqual(collected_on, [large_thread])
        trim.assert_called_once_with(0)


if __name__ == "__main__":
    unittest.main()
