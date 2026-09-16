"""Scoring edge cases: no network and no model-assisted judgments."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('reader', Path(__file__).with_name('evaluation-expanded-reader.py'))
reader = importlib.util.module_from_spec(spec); spec.loader.exec_module(reader)


class ScoreTest(unittest.TestCase):
    def score(self, raw, answers, dataset='squad'):
        return reader.score(raw, {'answers': answers, 'dataset': dataset})

    def test_all_reference_aliases_are_used(self):
        self.assertEqual(self.score('{"answer":"The UK"}', ['Britain', 'UK'])['em'], 1)

    def test_no_answer_requires_explicit_sentinel(self):
        self.assertEqual(self.score('{"answer":"NO_ANSWER"}', [])['f1'], 1)
        self.assertEqual(self.score('{"answer":"unknown fact"}', [])['f1'], 0)

    def test_invalid_json_is_in_denominator(self):
        self.assertFalse(self.score('not json', ['yes'])['format_valid'])
        self.assertEqual(self.score('not json', ['yes'])['em'], 0)

    def test_yes_no_partial_overlap_does_not_get_credit(self):
        self.assertEqual(self.score('{"answer":"yes and no"}', ['yes'], 'hotpot')['f1'], 0)

    def test_chinese_partial_overlap(self):
        result = self.score('{"answer":"北京大学"}', ['北京'], 'cmrc')
        self.assertEqual(result['em'], 0)
        self.assertAlmostEqual(result['f1'], 2 / 3)


if __name__ == '__main__':
    unittest.main()
