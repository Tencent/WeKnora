import importlib.util
import json
import os
import pathlib
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("collector", pathlib.Path(__file__).with_name("collect-status.py"))
collector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(collector)


class CollectorTest(unittest.TestCase):
    def test_backup_marker_and_boundary(self):
        now = int(time.time())
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as outside:
            root = pathlib.Path(directory)
            snapshot = root / "snapshot"
            snapshot.mkdir()
            (root / "latest").symlink_to(snapshot)
            self.assertFalse(collector.backup("test", directory, now)["available"])
            marker = snapshot / "COMPLETE"
            marker.touch()
            self.assertTrue(collector.backup("test", directory, now)["available"])
            os.utime(marker, (now - 27 * 3600, now - 27 * 3600))
            self.assertTrue(collector.backup("test", directory, now)["stale"])
            os.utime(marker, (now + 120, now + 120))
            self.assertFalse(collector.backup("test", directory, now)["available"])
            (root / "latest").unlink()
            (root / "latest").symlink_to(outside)
            (pathlib.Path(outside) / "COMPLETE").touch()
            self.assertFalse(collector.backup("test", directory, now)["available"])

    def test_missing_service_remains_unknown(self):
        with patch.object(collector, "command", return_value="LoadState=not-found\nActiveState=inactive\nResult=success\nExecMainStatus=0"):
            self.assertFalse(collector.service("missing.service", False)["available"])

    def test_collection_failures_remain_unknown(self):
        with patch.object(collector, "command", side_effect=OSError("unavailable")):
            result = collector.collect(services=["job.service"], containers=["app"])
        self.assertFalse(result["services"][0]["available"])
        self.assertNotIn("status", result["logs"][0])

    def test_container_failures_are_independent_and_rotations_counted(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "log.json"
            path.write_bytes(b"123")
            pathlib.Path(str(path) + ".1").write_bytes(b"4567")
            data = [{"LogPath": str(path), "HostConfig": {"LogConfig": {"Config": {"max-size": "20m"}}}, "State": {"Status": "running"}}]
            with patch.object(collector, "command", side_effect=[OSError("missing"), json.dumps(data)]):
                result = collector.collect(containers=["missing", "app"])
            self.assertNotIn("status", result["logs"][0])
            self.assertEqual(result["logs"][1]["bytes"], 7)
            self.assertEqual(result["logs"][1]["status"], "running")

    def test_atomic_write_replaces_complete_sample(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "status.json"
            collector.write_atomic(path, {"collected_at": 10})
            collector.write_atomic(path, {"collected_at": 20})
            self.assertEqual(json.loads(path.read_text()), {"collected_at": 20})
            self.assertEqual(list(path.parent.glob(".status-*")), [])


if __name__ == "__main__":
    unittest.main()
