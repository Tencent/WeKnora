import importlib.util
import json
import os
import pathlib
import unittest
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("topic3.py")
SPEC = importlib.util.spec_from_file_location("topic3", MODULE_PATH)
topic3 = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(topic3)


class AuthHeaderTest(unittest.TestCase):
    def test_api_key_uses_x_api_key(self):
        self.assertEqual(topic3.auth_header("sk-example"), ("X-API-Key", "sk-example"))

    def test_jwt_uses_bearer(self):
        self.assertEqual(
            topic3.auth_header("jwt-value"),
            ("Authorization", "Bearer jwt-value"),
        )


class AllRunnerSafetyTest(unittest.TestCase):
    def test_windows_run_logged_resolves_npm_cmd(self):
        with mock.patch.object(topic3.shutil, "which", return_value=r"D:\Node\npm.cmd"):
            command = topic3.resolve_command(["npm", "run", "type-check"], "nt")
        self.assertEqual(command[0], r"D:\Node\npm.cmd")

    def test_usage_cost_separates_cached_tokens(self):
        usage = {
            "prompt_tokens": 2000,
            "completion_tokens": 100,
            "cache_read_tokens": 1000,
            "cache_reported": True,
        }
        with mock.patch.dict(os.environ, {
            "TOPIC3_CHAT_INPUT_PRICE_PER_MILLION_CNY": "0.2",
            "TOPIC3_CHAT_CACHED_INPUT_PRICE_PER_MILLION_CNY": "0.04",
            "TOPIC3_CHAT_OUTPUT_PRICE_PER_MILLION_CNY": "0.8",
        }):
            self.assertAlmostEqual(topic3.usage_cost(usage), 0.00032)

    def test_validate_cache_row_requires_ten_completed_questions(self):
        with self.assertRaises(RuntimeError):
            topic3.validate_cache_row({
                "task": {"total": 10, "finished": 9, "status": 2},
                "result": "result.json", "phase": "warm", "repetition": 1,
            })

    def test_api_key_is_only_sent_in_header(self):
        request_seen = {}

        class FakeResponse:
            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def read(self):
                return b'{"success":true,"data":{}}'

        def fake_urlopen(request, timeout):
            request_seen["request"] = request
            return FakeResponse()

        secret = "sk-example-secret-value-1234567890"
        with mock.patch.dict(os.environ, {"TOPIC3_TOKEN": secret, "TOPIC3_BASE_URL": "http://localhost:8080"}), \
                mock.patch.object(topic3.urllib.request, "urlopen", fake_urlopen):
            topic3.api("POST", "/api/v1/example", {"safe": "value"})
        request = request_seen["request"]
        self.assertEqual(request.get_header("X-api-key"), secret)
        self.assertNotIn(secret, request.full_url)
        self.assertNotIn(secret, request.data.decode("utf-8"))


if __name__ == "__main__":
    unittest.main()
