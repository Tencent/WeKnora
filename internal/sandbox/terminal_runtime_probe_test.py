"""Run with python3 -I terminal_runtime_probe_test.py."""

import importlib.util
import pathlib
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "terminal_runtime_probe",
    pathlib.Path(__file__).with_name("terminal_runtime_probe.py"),
)
probe = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(probe)


class TerminalRuntimeProbeTest(unittest.TestCase):
    def test_current_linux_runtime_satisfies_contract(self):
        with tempfile.TemporaryDirectory(prefix="runtime-probe-") as root:
            with mock.patch.object(probe, "ROOT", root):
                self.assertEqual("", probe.probe(probe.TERMINAL_CHECKS))
                self.assertEqual("", probe.probe(probe.FILE_CHECKS))

    def test_reports_each_required_component(self):
        for missing in ("bash", "proc", "prctl"):
            with self.subTest(missing=missing):
                checks = tuple(
                    (name, lambda name=name: name != missing)
                    for name, _check in probe.TERMINAL_CHECKS
                )
                self.assertEqual(missing, probe.probe(checks))

        checks = (("proc", lambda: True), ("renameat2", lambda: False))
        self.assertEqual("renameat2", probe.probe(checks))

    def test_bash_check_rejects_missing_binary(self):
        with mock.patch.object(probe.os.path, "isfile", return_value=False):
            self.assertFalse(probe.check_bash())

    def test_proc_check_rejects_missing_proc_mount(self):
        with mock.patch.object(probe.os, "listdir", return_value=[]):
            self.assertFalse(probe.check_proc())

    def test_prctl_check_rejects_unsupported_kernel(self):
        class UnsupportedLibc:
            @staticmethod
            def prctl(*_args):
                return -1

        with mock.patch.object(probe.ctypes, "CDLL", return_value=UnsupportedLibc()):
            self.assertFalse(probe.check_prctl())


if __name__ == "__main__":
    unittest.main(verbosity=2)
