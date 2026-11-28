import io
import tempfile
import unittest
import zipfile
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from docreader.memory_admission import (
    MIB, MemoryAdmission, MemoryCapacityWait, container_memory_working_set, estimate_parse_memory,
)


class MemoryAdmissionTest(unittest.TestCase):
    def test_concurrent_reservations_use_one_shared_budget(self):
        gate = MemoryAdmission(4608, 4096, lambda: 200 * MIB)

        def reserve(weight):
            try:
                return gate.reserve(weight)
            except MemoryCapacityWait:
                return 0

        with ThreadPoolExecutor(max_workers=40) as workers:
            reservations = list(workers.map(reserve, [1024 * MIB] * 40))
        self.assertEqual(sum(bool(value) for value in reservations), 4)
        for reservation in reservations:
            gate.release(reservation)
        with ThreadPoolExecutor(max_workers=40) as workers:
            reservations = list(workers.map(reserve, [64 * MIB] * 40))
        self.assertTrue(all(reservations))

    def test_reads_the_process_cgroup_and_excludes_inactive_file_cache(self):
        for membership, usage_file, key in (
            ("0::/docker/docreader", "docker/docreader/memory.current", "inactive_file"),
            ("6:memory:/docker/docreader", "memory/docker/docreader/memory.usage_in_bytes", "total_inactive_file"),
        ):
            with tempfile.TemporaryDirectory() as directory, self.subTest(membership=membership):
                root = Path(directory)
                proc = root / "cgroup"
                proc.write_text(membership)
                usage = root / usage_file
                usage.parent.mkdir(parents=True)
                usage.write_text("500")
                (usage.parent / "memory.stat").write_text(f"anon 350\n{key} 150\n")
                self.assertEqual(container_memory_working_set(root, proc), 350)


    def test_watermarks_and_cancellation(self):
        usage = 100
        gate = MemoryAdmission(100, 60, lambda: usage * MIB)
        with self.assertRaises(MemoryCapacityWait):
            gate.reserve(MIB)
        usage = 61
        with self.assertRaises(MemoryCapacityWait):
            gate.reserve(MIB)
        usage = 60
        with self.assertRaises(GeneratorExit), gate.hold(gate.reserve(40 * MIB)):
            raise GeneratorExit
        self.assertEqual(gate.reserve(40 * MIB), 40 * MIB)

    def test_xlsx_uses_uncompressed_size(self):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as archive:
            archive.writestr("xl/worksheets/sheet1.xml", b"0" * MIB)
        self.assertEqual(estimate_parse_memory("test.xlsx", "", len(buf.getvalue()), buf.getvalue()), 136 * MIB)

    def test_cgroup_required_only_when_enabled(self):
        def unavailable():
            raise OSError("missing")
        self.assertEqual(MemoryAdmission(0, 0, unavailable).reserve(MIB), 0)
        with self.assertRaisesRegex(ValueError, "cgroup"):
            MemoryAdmission(128, 64, unavailable)
