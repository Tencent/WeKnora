import os
import signal
import subprocess
import unittest
from unittest.mock import patch

from docreader.container_entrypoint import runtime_environment


class ContainerEntrypointTest(unittest.TestCase):
    def setUp(self):
        env = patch.dict(os.environ, {"PATH": "/app/.venv/bin"}, clear=True)
        env.start()
        self.addCleanup(env.stop)
        machine = patch("docreader.container_entrypoint.platform.machine", return_value="aarch64")
        machine.start()
        self.addCleanup(machine.stop)

    @patch("docreader.container_entrypoint.subprocess.run")
    def test_healthy_arm_keeps_acceleration(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, b"", b"")
        self.assertNotIn("OPENSSL_armcap", runtime_environment())
        self.assertEqual(run.call_count, 1)

    @patch("docreader.container_entrypoint.subprocess.run")
    def test_sigill_retries_and_verifies_portable_crypto(self, run):
        run.side_effect = [subprocess.CompletedProcess([], -signal.SIGILL, b"", b""), subprocess.CompletedProcess([], 0, b"", b"")]
        self.assertEqual(runtime_environment()["OPENSSL_armcap"], "0")
        self.assertEqual(run.call_count, 2)
        self.assertNotIn("OPENSSL_armcap", os.environ)

    @patch("docreader.container_entrypoint.subprocess.run")
    def test_other_failure_is_not_masked(self, run):
        run.return_value = subprocess.CompletedProcess([], 1, b"", b"missing dependency")
        with self.assertRaisesRegex(RuntimeError, "missing dependency"):
            runtime_environment()
        self.assertEqual(run.call_count, 1)

    @patch("docreader.container_entrypoint.subprocess.run")
    def test_failed_portable_probe_stays_fatal(self, run):
        run.side_effect = [subprocess.CompletedProcess([], -signal.SIGILL, b"", b""), subprocess.CompletedProcess([], 1, b"", b"encryption failed")]
        with self.assertRaisesRegex(RuntimeError, "encryption failed"):
            runtime_environment()

    @patch("docreader.container_entrypoint.subprocess.run")
    def test_operator_override_is_preserved(self, run):
        os.environ["OPENSSL_armcap"] = "0x1"
        self.assertEqual(runtime_environment()["OPENSSL_armcap"], "0x1")
        run.assert_not_called()

    @patch("docreader.container_entrypoint.platform.machine", return_value="x86_64")
    @patch("docreader.container_entrypoint.subprocess.run")
    def test_other_architectures_are_unchanged(self, run, machine):
        self.assertNotIn("OPENSSL_armcap", runtime_environment())
        run.assert_not_called()

    @patch("docreader.container_entrypoint.subprocess.run", side_effect=subprocess.TimeoutExpired("probe", 30))
    def test_probe_has_bounded_startup(self, run):
        with self.assertRaises(subprocess.TimeoutExpired):
            runtime_environment()


if __name__ == "__main__":
    unittest.main()
