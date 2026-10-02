"""Offline regression tests for the actual WeKnora QA-generation call chain.

Uses real pandas/Parquet input and output, but never a real OpenAI client.
"""

import argparse
from contextlib import redirect_stderr, redirect_stdout
import importlib.util
import io
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import pandas as pd


SOURCE = Path(__file__).resolve().parents[1] / "qa_dataset.py"
LEGACY_DEFAULT = "gpt-4o-2024-05-13"
EXPLICIT_MODEL = "synthetic-compatible-model"


class QADatasetModelTests(unittest.TestCase):
    def enter_context(self, manager):
        value = manager.__enter__()
        self.addCleanup(manager.__exit__, None, None, None)
        return value

    def setUp(self):
        spec = importlib.util.spec_from_file_location("qa_dataset_under_test", SOURCE)
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.input_dir = Path(self.directory.name) / "input"
        self.output_dir = Path(self.directory.name) / "output"
        self.input_dir.mkdir()
        self.queries = pd.DataFrame([
            {"id": "q1", "text": "What color is the synthetic box?"},
            {"id": "q2", "text": "What shape is the synthetic tile?"},
        ])
        self.corpus = pd.DataFrame([
            {"id": "p1", "text": "The synthetic box is blue."},
            {"id": "p2", "text": "The synthetic tile is square."},
        ])
        self.qrels = pd.DataFrame([
            {"qid": "q1", "pid": "p1"},
            {"qid": "q2", "pid": "p2"},
        ])
        for name, frame in (("queries", self.queries), ("corpus", self.corpus),
                            ("qrels", self.qrels)):
            frame.to_parquet(self.input_dir / f"{name}.parquet")
        self.client = Mock()
        self.client.chat.completions.create.return_value = SimpleNamespace(
            choices=[SimpleNamespace(message=SimpleNamespace(content="synthetic answer"))]
        )
        self.client_factory = self.enter_context(
            patch.object(self.module.openai, "Client", return_value=self.client)
        )
        self.enter_context(patch.dict("os.environ", {
            "OPENAI_API_KEY": "local-test-placeholder",
            "OPENAI_BASE_URL": "https://example.invalid/v1",
        }))
        self.enter_context(patch("socket.create_connection", side_effect=AssertionError("network forbidden")))
        self.enter_context(patch("socket.socket.connect", side_effect=AssertionError("network forbidden")))
        self.enter_context(redirect_stdout(io.StringIO()))

    def generated_models(self):
        return [call.kwargs["model"] for call in self.client.chat.completions.create.call_args_list]

    def assert_real_output(self, count=2):
        answers = pd.read_parquet(self.output_dir / "answers.parquet")
        pairs = pd.read_parquet(self.output_dir / "qas.parquet")
        self.assertEqual(len(answers), count)
        self.assertEqual(len(pairs), count)
        self.assertEqual(answers["text"].tolist(), ["synthetic answer"] * count)

    def run_cli(self, *extra):
        argv = [str(SOURCE), "generate", "--input_dir", str(self.input_dir),
                "--output_dir", str(self.output_dir), *extra]
        with patch.object(sys, "argv", argv):
            self.module.main()

    def test_direct_answer_default_is_unchanged(self):
        system = self.module.QAAnsweringSystem(self.queries, self.corpus, self.qrels)
        self.assertEqual(system.answer_question("q1"), "synthetic answer")
        self.assertEqual(self.generated_models(), [LEGACY_DEFAULT])

    def test_existing_direct_answer_override_remains_supported(self):
        system = self.module.QAAnsweringSystem(self.queries, self.corpus, self.qrels)
        system.answer_question("q1", model=EXPLICIT_MODEL)
        self.assertEqual(self.generated_models(), [EXPLICIT_MODEL])

    def test_generate_default_retains_legacy_model(self):
        self.module.generate_answers(str(self.input_dir), str(self.output_dir))
        self.assertEqual(self.generated_models(), [LEGACY_DEFAULT, LEGACY_DEFAULT])
        self.assert_real_output()

    def test_generate_explicit_model_reaches_actual_client(self):
        self.module.generate_answers(str(self.input_dir), str(self.output_dir), model=EXPLICIT_MODEL)
        self.assertEqual(self.generated_models(), [EXPLICIT_MODEL, EXPLICIT_MODEL])
        self.assert_real_output()
        sent = self.client.chat.completions.create.call_args_list[0].kwargs
        self.assertIn("The synthetic box is blue.", sent["messages"][0]["content"])
        self.assertIn("What color is the synthetic box?", sent["messages"][0]["content"])
        self.assertEqual(sent["temperature"], 0.3)

    def test_cli_without_model_retains_default(self):
        self.run_cli()
        self.assertEqual(self.generated_models(), [LEGACY_DEFAULT, LEGACY_DEFAULT])
        self.assert_real_output()

    def test_cli_model_reaches_real_generation_chain(self):
        self.run_cli("--model", EXPLICIT_MODEL)
        self.assertEqual(self.generated_models(), [EXPLICIT_MODEL, EXPLICIT_MODEL])
        self.assert_real_output()
        self.client_factory.assert_called_once_with(
            api_key="local-test-placeholder", base_url="https://example.invalid/v1"
        )

    def test_cli_help_exposes_model_option_without_calling_client(self):
        captured = io.StringIO()
        with patch.object(sys, "argv", [str(SOURCE), "generate", "--help"]), redirect_stdout(captured):
            with self.assertRaises(SystemExit) as stopped:
                self.module.main()
        self.assertEqual(stopped.exception.code, 0)
        self.assertIn("--model", captured.getvalue())
        help_text = " ".join(captured.getvalue().split())
        self.assertIn("saved answers", help_text)
        self.assertIn("new --output_dir", help_text)
        self.client_factory.assert_not_called()

    def assert_invalid_cli_model(self, value):
        errors = io.StringIO()
        with patch.object(self.module, "read_parquet") as read_input:
            with redirect_stderr(errors), self.assertRaises(SystemExit) as stopped:
                self.run_cli("--model", value)
        self.assertEqual(stopped.exception.code, 2)
        self.assertIn("model must not be empty or whitespace-only", errors.getvalue())
        read_input.assert_not_called()
        self.client_factory.assert_not_called()
        self.client.chat.completions.create.assert_not_called()
        self.assertFalse(self.output_dir.exists())

    def test_cli_rejects_empty_model_before_loading_or_provider(self):
        self.assert_invalid_cli_model("")

    def test_cli_rejects_whitespace_model_before_loading_or_provider(self):
        for value in ("   ", "\t", "\n"):
            with self.subTest(value=repr(value)):
                self.assert_invalid_cli_model(value)

    def test_existing_third_positional_retry_argument_remains_valid(self):
        self.module.generate_answers(str(self.input_dir), str(self.output_dir), 0)
        self.assertEqual(self.generated_models(), [LEGACY_DEFAULT, LEGACY_DEFAULT])
        self.assert_real_output()

    def test_explicit_model_does_not_change_resume_behavior(self):
        self.output_dir.mkdir()
        pd.DataFrame([{"id": 1, "text": "existing answer"}]).to_parquet(self.output_dir / "answers.parquet")
        pd.DataFrame([{"qid": "q1", "aid": 1}]).to_parquet(self.output_dir / "qas.parquet")
        self.run_cli("--model", EXPLICIT_MODEL)
        self.assertEqual(self.generated_models(), [EXPLICIT_MODEL])
        saved = pd.read_parquet(self.output_dir / "answers.parquet")
        self.assertEqual(saved["text"].tolist(), ["existing answer", "synthetic answer"])


if __name__ == "__main__":
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--source", type=Path, default=SOURCE)
    options, remaining = parser.parse_known_args()
    SOURCE = options.source.resolve()
    unittest.main(argv=[sys.argv[0], *remaining])
