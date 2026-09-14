"""Test the runner's /proc sampling without launching commands on the host."""

import ast
import io
import os
import pathlib
import unittest
from unittest import mock

# Import only the sampling functions: the runner's top level requires a sandbox PTY.
source = ast.parse(pathlib.Path(__file__).with_name("terminal_runner.py").read_text())
sampling = ast.Module(body=[node for node in source.body
                           if isinstance(node, ast.FunctionDef)
                           and node.name in ("processes", "descendants")], type_ignores=[])
runner = {"os": os}
exec(compile(sampling, "terminal_runner.py", "exec"), runner)


class TerminalRunnerSamplingTest(unittest.TestCase):
    def test_rss_pages_are_distinct_from_cpu_ticks_and_virtual_size(self):
        fields = ["0"] * 22
        fields[0], fields[1], fields[19] = "S", "10", "1234"
        fields[11:15] = ["1", "2", "3", "4"]
        fields[20], fields[21] = "1000000", "23"
        stat = "11 (name with ) parentheses) " + " ".join(fields)
        with mock.patch.object(os, "listdir", return_value=["11", "self"]), \
                mock.patch("builtins.open", return_value=io.StringIO(stat)):
            self.assertEqual({11: (10, "1234", 10, 23)}, runner["processes"]())

    def test_disappearing_unreadable_and_malformed_processes_are_ignored(self):
        failures = [ProcessLookupError(), PermissionError(), io.StringIO("bad stat")]
        with mock.patch.object(os, "listdir", return_value=["1", "2", "3"]), \
                mock.patch("builtins.open", side_effect=failures):
            self.assertEqual({}, runner["processes"]())

    def test_tree_excludes_supervisor_and_unrelated_processes(self):
        table = {
            10: (1, "100", 1, 1000),  # Supervisor is not charged to the command.
            11: (10, "101", 2, 10),
            12: (11, "102", 3, 20),
            13: (10, "103", 4, 30),  # Adopted double-fork descendant.
            14: (1, "104", 5, 2000),
        }
        with mock.patch.object(os, "getpid", return_value=10):
            self.assertEqual({11, 12, 13}, runner["descendants"](table))
        self.assertEqual(60, sum(table[pid][3] for pid in (11, 12, 13)))


if __name__ == "__main__":
    unittest.main(verbosity=2)
