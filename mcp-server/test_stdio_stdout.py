#!/usr/bin/env python3
"""Regression: MCP stdio entry scripts must not write diagnostics to stdout."""

import subprocess
import sys
import unittest
from pathlib import Path

MCP_SERVER_DIR = Path(__file__).resolve().parent


class StdioStdoutPurityTest(unittest.TestCase):
    def test_main_check_only_keeps_stdout_empty(self):
        result = subprocess.run(
            [sys.executable, "main.py", "--check-only"],
            capture_output=True,
            timeout=10,
            cwd=MCP_SERVER_DIR,
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(
            result.stdout,
            b"",
            f"stdout must stay empty for MCP stdio compatibility, got: {result.stdout!r}",
        )
        self.assertTrue(
            result.stderr,
            "environment check output should appear on stderr",
        )

    def test_run_server_startup_keeps_stdout_empty(self):
        proc = subprocess.Popen(
            [sys.executable, "run_server.py"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=MCP_SERVER_DIR,
        )
        try:
            stdout, stderr = proc.communicate(timeout=2)
        except subprocess.TimeoutExpired:
            proc.terminate()
            stdout, stderr = proc.communicate(timeout=5)

        self.assertEqual(
            stdout,
            b"",
            f"run_server.py must not write to stdout before JSON-RPC, got: {stdout!r}",
        )
        self.assertTrue(stderr, "startup diagnostics should appear on stderr")


if __name__ == "__main__":
    unittest.main()
