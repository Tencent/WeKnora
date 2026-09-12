"""Regression checks for benchmark leakage, missing-run accounting and result integrity."""
import hashlib
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("parser_score", ROOT / "scripts/score-parser-benchmark.py")
score = importlib.util.module_from_spec(spec)
spec.loader.exec_module(score)


class ScoringIntegrityTests(unittest.TestCase):
    def test_image_alt_and_filename_are_not_recognition(self):
        self.assertEqual(score.extraction_state("![全是真值 fake reference text](images/all_ground_truth_123.png)", 1), ("image_only", 0))
        self.assertEqual(score.extraction_state('<img alt="recognized words" src="scan.png">'), ("image_only", 0))
        self.assertEqual(score.extraction_state("![alt][img]\n[img]: local/ground_truth_abc.png"), ("image_only", 0))

    def test_real_chinese_content_survives_image_removal(self):
        state, characters = score.extraction_state("![图片](p.png)\n# 实际正文 42", 1)
        self.assertEqual(state, "usable_text")
        self.assertEqual(characters, 6)

    def test_official_export_image_cleanup_preserves_table_formula_and_footnote(self):
        original = "![invented words](plot-123.png)\n<table><tr><td>42</td></tr></table>\n$$x^2$$\n图1真实说明\n[^n]:真实脚注"
        cleaned = score.strip_image_references(original)
        self.assertNotIn("invented", cleaned)
        self.assertNotIn("123", cleaned)
        for text in ("<table><tr><td>42</td></tr></table>", "$$x^2$$", "图1真实说明", "[^n]:真实脚注"):
            self.assertIn(text, cleaned)

    def test_normalization_is_deterministic_across_line_endings_and_has_provenance(self):
        windows = "# 正文\r\n![not OCR](local.png)\r\n$$x^2$$\r\n"
        unix = windows.replace("\r\n", "\n")
        self.assertEqual(score.strip_image_references(windows), score.strip_image_references(unix))
        provenance = score.normalization_provenance()
        self.assertEqual(provenance["version"], "image-reference-cleanup-v1")
        self.assertEqual(len(provenance["function_source_sha256"]), 64)
        self.assertIn("leaderboard", provenance["leaderboard_input_difference"])

    def test_missing_run_is_not_counted_as_a_completed_failure(self):
        rows = [dict(status="success", extraction_state="image_only", duration_ms=8, fallback=None),
                dict(status="not_run", extraction_state="not_run", duration_ms=None, fallback=None)]
        result = score.aggregate(rows)
        self.assertEqual(result["expected_pages"], 2)
        self.assertEqual(result["executed_pages"], 1)
        self.assertEqual(result["protocol_success_rate_on_executed"], 1)
        self.assertEqual(result["usable_text_rate_on_executed"], 0)
        self.assertIsNone(result["olmocr"]["micro_pass_rate"])

    def test_evaluator_error_is_not_a_pass_or_model_failure(self):
        def broken_loader(_):
            raise RuntimeError("missing runtime")
        result = score.run_olm_tests({"tests": [{"id": "x", "type": "math"}]}, "answer", broken_loader)
        self.assertEqual(result["evaluator_errors"], 1)
        self.assertEqual(result["scored"], 0)
        self.assertIsNone(result["pass_rate"])

    def test_tampered_output_fails_integrity_before_scoring(self):
        with tempfile.TemporaryDirectory() as directory:
            runs = Path(directory)
            folder = runs / "builtin"
            folder.mkdir()
            (folder / "page.md").write_text("changed", encoding="utf-8")
            (folder / "page.json").write_text(json.dumps({"engine": "builtin", "sample_id": "page", "status": "success",
                "input_sha256": "expected", "markdown_sha256": hashlib.sha256(b"original").hexdigest()}))
            sample = dict(id="page", source="OmniDocBench", category="book", language="english", input_kind="image_wrapped_pdf",
                          input_details={"has_text_layer": False}, pdf_path="unused.pdf", sha256="expected")
            result = score.audit_sample(sample, "builtin", runs)
            self.assertEqual(result["status"], "integrity_error")
            self.assertIn("markdown_sha256", result["integrity"])

    def test_true_fallback_metadata_is_preserved(self):
        self.assertEqual(score.extract_metadata({"metadata": {"parser_fallback": "builtin_scanned_renderer"}})["parser_fallback"], "builtin_scanned_renderer")


class ManifestMetadataTests(unittest.TestCase):
    def test_frozen_manifests_preserve_unique_inputs_and_separate_references(self):
        full = score.read_json(ROOT / "dataset/parser-benchmark/manifest-full.json")["samples"]
        smoke = score.read_json(ROOT / "dataset/parser-benchmark/manifest-smoke.json")["samples"]
        index = {row["id"]: row for row in full}
        self.assertEqual(len(full), 100)
        self.assertEqual(len(index), 100)
        for row in smoke:
            self.assertEqual(row, index[row["id"]])
        for row in full:
            self.assertNotEqual(row["pdf_path"], row["reference"]["path"])
            self.assertRegex(row["sha256"], r"^[0-9a-f]{64}$")
            self.assertRegex(row["reference"]["sha256"], r"^[0-9a-f]{64}$")
            self.assertFalse(Path(row["pdf_path"]).is_absolute())
            self.assertFalse(Path(row["reference"]["path"]).is_absolute())
            self.assertNotIn("..", Path(row["pdf_path"]).parts)
            self.assertNotIn("..", Path(row["reference"]["path"]).parts)


class DatasetIntegrityTests(unittest.TestCase):
    def test_smoke_is_exact_subset_of_full_and_references_are_separate(self):
        full_path = ROOT / "dataset/parser-benchmark/manifest-full.json"
        if not full_path.exists():
            self.skipTest("Prepare public dataset first")
        full = score.read_json(full_path)["samples"]
        smoke = score.read_json(ROOT / "dataset/parser-benchmark/manifest-smoke.json")["samples"]
        self.assertEqual(len(full), 100)
        self.assertEqual(len({r["id"] for r in full}), 100)
        index = {r["id"]: r for r in full}
        for row in smoke:
            self.assertEqual(row, index[row["id"]])
        for row in full:
            self.assertNotEqual(row["reference"]["path"], row["pdf_path"])
            self.assertEqual(score.sha256(ROOT / row["pdf_path"]), row["sha256"])
            self.assertEqual(score.sha256(ROOT / row["reference"]["path"]), row["reference"]["sha256"])

    def test_no_ground_truth_text_layer_in_omni_inputs(self):
        from pypdf import PdfReader
        path = ROOT / "dataset/parser-benchmark/manifest-full.json"
        if not path.exists():
            self.skipTest("Prepare public dataset first")
        for row in score.read_json(path)["samples"]:
            if row["source"] == "OmniDocBench":
                pdf = PdfReader(ROOT / row["pdf_path"])
                self.assertEqual(len(pdf.pages), 1)
                self.assertFalse(pdf.pages[0].extract_text(), row["id"])


class OfficialDenominatorAuditTests(unittest.TestCase):
    def test_missing_official_page_is_exposed_without_fabricating_a_score(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            folder = output / "official-omnidocbench"
            result = folder / "result"
            result.mkdir(parents=True)
            (folder / "builtin").mkdir()
            score.write_json(folder / "ground_truth.json", [
                {"page_info": {"image_path": name}, "layout_dets": [{"category_type": "text_block", "order": 1}]}
                for name in ("a.jpg", "b.jpg")])
            inputs = []
            for name in ("a", "b"):
                (folder / "builtin" / f"{name}.md").write_text("" if name == "b" else "text")
                inputs.append({"source_sample_id": f"{name}.jpg", "scoring_filename": f"{name}.md",
                               "error_as_empty_output": name == "b"})
            score.write_json(folder / "builtin-input-manifest.json", {"samples": inputs})
            score.write_json(result / "builtin_quick_match_run_summary.json", {
                "page_denominators": {"text_block": {"Edit_dist": {"ALL": 2}}}})
            score.write_json(result / "builtin_quick_match_text_block_result.json", [
                {"img_id": "a.jpg", "metric": {"Edit_dist": 0.2}}])
            score.write_json(result / "builtin_quick_match_text_block_per_page_edit.json", {"a.jpg": 0.2})
            exports = [{"engine": "builtin", "ready_for_official_scoring": True,
                        "input_manifest_path": str(folder / "builtin-input-manifest.json")}]
            audit = score.audit_official_denominators(exports, output, {"builtin": {"status": "completed"}})
            metric = audit["engines"]["builtin"]["metrics"]["text_block"]
            self.assertEqual(metric["official_reported_page_denominator"], 2)
            self.assertEqual(metric["actual_metric_page_denominator"], 1)
            self.assertEqual(metric["gt_pages_excluded_from_metric"], ["b.jpg"])
            self.assertEqual(metric["failed_prediction_gt_pages"], 1)
            self.assertEqual(metric["failed_prediction_gt_pages_in_metric"], 0)
            self.assertFalse(metric["denominator_matches_actual"])
            self.assertEqual(score.read_json(result / "builtin_quick_match_text_block_per_page_edit.json"), {"a.jpg": 0.2})


class ReviewBindingTests(unittest.TestCase):
    def queue(self, markdown_hash="output-a", record_hash="run-a", status="success"):
        sample = dict(id="page", sha256="input", reference={"sha256": "reference"}, source="OmniDocBench",
                      category="book", language="english", pdf_path="input.pdf")
        row = dict(sample_id="page", engine="builtin", status=status, markdown_sha256=markdown_hash,
                   run_record_sha256=record_hash, extraction_state="usable_text", markdown_path=str(ROOT / "output.md"), fallback=None)
        return score.human_review_queue({"samples": [sample]}, [row])

    def signed(self):
        queue = self.queue()
        queue["items"][0].update(status="completed", reviewer="real reviewer", reviewed_at="2026-09-11", decision="accepted", notes="checked")
        return queue

    def test_signature_preserved_only_for_identical_result_fingerprint(self):
        merged = score.merge_previous_reviews(self.queue(), self.signed())
        self.assertEqual(merged["human_review_completed"], 1)
        self.assertEqual(merged["items"][0]["reviewer"], "real reviewer")

    def test_output_change_invalidates_review_and_archives_signature(self):
        merged = score.merge_previous_reviews(self.queue(markdown_hash="output-b"), self.signed())
        self.assertEqual(merged["human_review_completed"], 0)
        row = merged["items"][0]
        self.assertEqual(row["status"], "pending")
        self.assertIsNone(row["reviewer"])
        self.assertEqual(row["review_history"][0]["reviewer"], "real reviewer")

    def test_run_metadata_or_pending_completion_changes_review_fingerprint(self):
        original = self.queue()["items"][0]["review_fingerprint"]
        self.assertNotEqual(original, self.queue(record_hash="new-model-run")["items"][0]["review_fingerprint"])
        self.assertNotEqual(original, self.queue(status="not_run")["items"][0]["review_fingerprint"])

    def test_legacy_unbound_signature_is_not_reused(self):
        old = self.signed()
        del old["items"][0]["review_fingerprint"]
        merged = score.merge_previous_reviews(self.queue(), old)
        self.assertEqual(merged["items"][0]["status"], "pending")
        self.assertEqual(merged["items"][0]["review_history"][0]["stale_reason"], "legacy_review_without_output_fingerprint")

    def test_stale_history_survives_a_second_unreviewed_update(self):
        first = score.merge_previous_reviews(self.queue(markdown_hash="b"), self.signed())
        second = score.merge_previous_reviews(self.queue(markdown_hash="c"), first)
        self.assertEqual(len(second["items"][0]["review_history"]), 1)
        self.assertEqual(second["items"][0]["review_history"][0]["reviewer"], "real reviewer")


if __name__ == "__main__":
    unittest.main()
