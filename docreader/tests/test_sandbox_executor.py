import os
import shlex
import signal
import subprocess
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import ANY, Mock, call, patch

from docreader.parser.doc_parser import SandboxExecutor


@unittest.skipUnless(os.name == "posix", "process-group cleanup is POSIX-specific")
class TestSandboxExecutorProcessGroup(unittest.TestCase):
    def test_timeout_kills_whole_process_group(self):
        """A timed-out command must terminate its parent and descendants."""
        with tempfile.TemporaryDirectory() as temp_dir:
            child_pid_file = Path(temp_dir) / "child.pid"
            command = [
                "sh",
                "-c",
                f"sleep 30 & echo $! > {shlex.quote(str(child_pid_file))}; wait",
            ]
            executor = SandboxExecutor(default_timeout=1)

            with self.assertRaises(RuntimeError):
                executor.execute_in_sandbox(command)

            child_pid = int(child_pid_file.read_text().strip())
            deadline = time.monotonic() + 1
            while True:
                try:
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    break
                if time.monotonic() >= deadline:
                    self.fail("timed-out command left a child process running")
                time.sleep(0.01)


class TestSandboxExecutorMock(unittest.TestCase):
    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_terminates_group_and_reaps_process(self):
        process = Mock(pid=4321)
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 1),
            subprocess.TimeoutExpired(["soffice"], 0.5),
            (b"", b""),
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch(
                "docreader.parser.doc_parser.subprocess.Popen", return_value=process
            ) as popen,
            patch("docreader.parser.doc_parser.os.killpg") as killpg,
            patch(
                "docreader.parser.doc_parser.time.monotonic", side_effect=[0, 0.5]
            ),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
        ):
            with self.assertRaisesRegex(RuntimeError, "timeout after 1"):
                SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        popen.assert_called_once_with(
            ["soffice"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=ANY,
            start_new_session=True,
        )
        killpg.assert_has_calls(
            [
                call(4321, signal.SIGTERM),
                call(4321, signal.SIGKILL),
            ]
        )
        sleep.assert_not_called()
        self.assertEqual(
            process.communicate.call_args_list,
            [
                call(timeout=1),
                call(timeout=0.5),
                call(),
            ],
        )

    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_waits_full_grace_period_when_child_exits_early(self):
        process = Mock(pid=4321)
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 1),
            (b"", b""),
            (b"", b""),
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch(
                "docreader.parser.doc_parser.subprocess.Popen", return_value=process
            ),
            patch("docreader.parser.doc_parser.os.killpg") as killpg,
            patch(
                "docreader.parser.doc_parser.time.monotonic", side_effect=[10, 10.2]
            ),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
        ):
            with self.assertRaisesRegex(RuntimeError, "timeout after 1"):
                SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        self.assertAlmostEqual(sleep.call_args.args[0], 0.3)
        killpg.assert_has_calls(
            [
                call(4321, signal.SIGTERM),
                call(4321, signal.SIGKILL),
            ]
        )
        self.assertEqual(
            process.communicate.call_args_list,
            [
                call(timeout=1),
                call(timeout=0.5),
                call(),
            ],
        )

    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_reaps_when_group_already_exited(self):
        process = Mock(pid=4321)
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 1),
            subprocess.TimeoutExpired(["soffice"], 0.5),
            (b"", b""),
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch(
                "docreader.parser.doc_parser.subprocess.Popen", return_value=process
            ),
            patch(
                "docreader.parser.doc_parser.os.killpg",
                side_effect=ProcessLookupError,
            ) as killpg,
            patch(
                "docreader.parser.doc_parser.time.monotonic", side_effect=[0, 0.5]
            ),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
        ):
            with self.assertRaisesRegex(RuntimeError, "timeout after 1"):
                SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        self.assertEqual(killpg.call_count, 2)
        sleep.assert_not_called()
        self.assertEqual(process.communicate.call_args_list[-1], call())

    def test_non_posix_timeout_kills_direct_child_and_reaps_process(self):
        process = Mock(pid=4321)
        process.communicate.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 1),
            (b"", b""),
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "nt"),
            patch(
                "docreader.parser.doc_parser.subprocess.Popen", return_value=process
            ) as popen,
            patch("docreader.parser.doc_parser.os.killpg", create=True) as killpg,
        ):
            with self.assertRaisesRegex(RuntimeError, "timeout after 1"):
                SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        popen.assert_called_once_with(
            ["soffice"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=ANY,
            start_new_session=False,
        )
        process.kill.assert_called_once_with()
        killpg.assert_not_called()
        self.assertEqual(process.communicate.call_args_list, [call(timeout=1), call()])


if __name__ == "__main__":
    unittest.main()
