#!/usr/bin/env python3
"""Offline integrity regressions for public dataset preparation (standard library)."""
import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("public_data", Path(__file__).with_name("prepare-public-evaluation.py"))
public = importlib.util.module_from_spec(spec)
spec.loader.exec_module(public)


class PublicDataIntegrityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="weknora-public-data-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / "v1"
        self.slug = "cmrc2018-dev"
        shutil.copytree(public.ROOT / "dataset/public" / self.slug / "v1", self.directory)

    def read(self, name):
        return json.loads((self.directory / name).read_bytes())

    def write_json(self, name, value, refresh_digest=False):
        data = public.encode(value)
        (self.directory / name).write_bytes(data)
        if refresh_digest:
            manifest = self.read("manifest.json")
            manifest["files_sha256"][name] = public.digest(data)
            (self.directory / "manifest.json").write_bytes(public.encode(manifest))

    def test_committed_packages_check_without_cache_or_network(self):
        with patch.object(public, "cached", side_effect=AssertionError("cache must not be read")), \
                patch.object(public, "urlopen", side_effect=AssertionError("network must not be used")):
            for slug in [*public.CONFIGS, "weknora-docs"]:
                result = public.check(slug, public.ROOT / "dataset/public" / slug / "v1")
                self.assertTrue(result["checked"])

    def test_unlisted_file_is_rejected(self):
        (self.directory / "source_docs/unlisted.txt").write_text("extra", encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "Missing/unlisted"):
            public.check(self.slug, self.directory)

    def test_byte_tamper_is_rejected(self):
        with (self.directory / "registry-input.json").open("ab") as out:
            out.write(b"\n")
        with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
            public.check(self.slug, self.directory)

    def test_unknown_relevance_even_with_updated_artifact_hash_is_rejected(self):
        content = self.read("registry-input.json")
        content["relevance"][0]["pid"] = "unknown-passage"
        self.write_json("registry-input.json", content, True)
        with self.assertRaisesRegex(ValueError, "Invalid relevance"):
            public.check(self.slug, self.directory)

    def test_wrong_answer_offset_even_with_updated_artifact_hash_is_rejected(self):
        sample = self.read("source-samples.json")
        sample["questions"][0]["qa"]["answers"][0]["answer_start"] = -1
        self.write_json("source-samples.json", sample, True)
        with self.assertRaisesRegex(ValueError, "Invalid original answer span"):
            public.check(self.slug, self.directory)

    def test_reference_answer_drift_even_with_updated_artifact_hash_is_rejected(self):
        content = self.read("registry-input.json")
        content["questions"][0]["answer"] = "invented answer"
        self.write_json("registry-input.json", content, True)
        with self.assertRaisesRegex(ValueError, "Registry transformation drift"):
            public.check(self.slug, self.directory)

    def test_changed_source_revision_is_rejected(self):
        manifest = self.read("manifest.json")
        manifest["upstream_files"][0]["revision"] = "main"
        self.write_json("manifest.json", manifest)
        with self.assertRaisesRegex(ValueError, "Pinned source manifest drift"):
            public.check(self.slug, self.directory)

    def test_changed_license_even_with_updated_artifact_hash_is_rejected(self):
        (self.directory / "LICENSE").write_bytes(b"MIT")
        manifest = self.read("manifest.json")
        manifest["files_sha256"]["LICENSE"] = public.digest(b"MIT")
        self.write_json("manifest.json", manifest)
        with self.assertRaisesRegex(ValueError, "Pinned license text drift"):
            public.check(self.slug, self.directory)

    def test_source_cache_tamper_is_rejected_without_network(self):
        cache = Path(self.temp.name) / "cache"
        cache.mkdir()
        (cache / "cmrc2018-dev.json").write_bytes(b"{}")
        with patch.object(public, "urlopen", side_effect=AssertionError("network must not be used")):
            with self.assertRaisesRegex(ValueError, "Pinned source SHA-256 mismatch"):
                public.cached("cmrc2018-dev.json", cache, download=True)

    def test_missing_cache_is_offline_by_default(self):
        with patch.object(public, "urlopen", side_effect=AssertionError("network must not be used")):
            with self.assertRaisesRegex(ValueError, "Missing pinned source"):
                public.cached("cmrc2018-dev.json", Path(self.temp.name) / "missing")

    def test_artifact_path_cannot_escape_dataset_directory(self):
        for name in ("../outside.txt", "../../outside.txt", str(Path(self.temp.name) / "outside.txt")):
            with self.assertRaisesRegex(ValueError, "leaves dataset directory"):
                public.checked_path(self.directory, name)

    def test_unanswerable_labels_are_scoped_to_original_context(self):
        directory = public.ROOT / "dataset/public/squad2-dev/v1"
        content = json.loads((directory / "registry-input.json").read_bytes())
        annotations = json.loads((directory / "annotations.json").read_bytes())
        impossible = [a for a in annotations if a["is_impossible"]]
        self.assertEqual(8, len(impossible))
        questions = {q["qid"]: q for q in content["questions"]}
        for annotation in impossible:
            edges = [e for e in content["relevance"] if e["qid"] == annotation["qid"]]
            self.assertEqual([{"qid": annotation["qid"], "pid": annotation["source_pid"], "grade": 0}], edges)
            self.assertEqual("", questions[annotation["qid"]]["answer"])
            self.assertEqual("source_pid_only", annotation["answerability_scope"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
