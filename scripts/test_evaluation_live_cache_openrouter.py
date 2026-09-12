import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("pricing", Path(__file__).with_name("evaluation-live-cache-openrouter.py"))
pricing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pricing)


class CostEvidenceTests(unittest.TestCase):
    rates = {"prompt": "0.00000003", "completion": "0.00000013", "input_cache_read": "0.000000006"}

    def test_cached_tokens_are_subtracted_from_normal_input(self):
        result = pricing.component_cost({"operation": "chat", "prompt_tokens": 1000,
                                         "output_tokens": 100, "cache_read_tokens": 800}, self.rates)
        self.assertEqual(result["subtotal_usd"], "0.000023800")
        self.assertIsNone(result["total_usd"])

    def test_no_quote_does_not_mean_free_but_application_hit_is_free(self):
        self.assertIsNone(pricing.component_cost({"operation": "embedding", "provider_requests": 1}, {})["subtotal_usd"])
        self.assertEqual(pricing.component_cost({"operation": "embedding", "provider_requests": 0,
                                                 "status": "success"}, {})["total_usd"], "0")

    def test_missing_and_invalid_usage(self):
        self.assertIsNone(pricing.component_cost({"operation": "chat"}, self.rates)["subtotal_usd"])
        with self.assertRaises(ValueError):
            pricing.component_cost({"operation": "chat", "prompt_tokens": 1,
                                    "output_tokens": 0, "cache_read_tokens": 2}, self.rates)

    def test_context_tier_changes_price(self):
        rates = {**self.rates, "overrides": [{"min_prompt_tokens": 32000, "prompt": "0.0000001"}]}
        result = pricing.component_cost({"operation": "chat", "prompt_tokens": 32000,
                                         "output_tokens": 0, "cache_read_tokens": 0}, rates)
        self.assertEqual(result["subtotal_usd"], "0.0032000")


if __name__ == "__main__":
    unittest.main()
