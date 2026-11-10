"""Regression test for the QA dataset model default."""

import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import pandas as pd


SOURCE = Path(__file__).resolve().parents[1] / "qa_dataset.py"


class QADatasetDefaultModelTests(unittest.TestCase):
    def test_default_model_reaches_openai_client(self):
        spec = importlib.util.spec_from_file_location("qa_dataset_under_test", SOURCE)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)

        client = Mock()
        client.chat.completions.create.return_value = SimpleNamespace(
            choices=[SimpleNamespace(message=SimpleNamespace(content="answer"))]
        )
        queries = pd.DataFrame([{"id": "q1", "text": "question"}])
        corpus = pd.DataFrame([{"id": "p1", "text": "context"}])
        qrels = pd.DataFrame([{"qid": "q1", "pid": "p1"}])

        with patch.object(module.openai, "Client", return_value=client):
            system = module.QAAnsweringSystem(queries, corpus, qrels)
            self.assertEqual(system.answer_question("q1"), "answer")

        request = client.chat.completions.create.call_args.kwargs
        self.assertEqual(request["model"], "gpt-5.6-sol")


if __name__ == "__main__":
    unittest.main()
