import os
import unittest
from unittest.mock import patch

from docreader.utils.ssrf import is_ssrf_safe_url, reset_ssrf_whitelist_cache_for_test


class TestSSRFValidation(unittest.TestCase):
    def setUp(self) -> None:
        self._env_patch = patch.dict(
            os.environ,
            {"SSRF_WHITELIST": "", "SSRF_WHITELIST_EXTRA": ""},
            clear=False,
        )
        self._env_patch.start()
        reset_ssrf_whitelist_cache_for_test()

    def tearDown(self) -> None:
        self._env_patch.stop()
        reset_ssrf_whitelist_cache_for_test()

    def test_blocks_loopback_ip(self):
        safe, reason = is_ssrf_safe_url("http://127.0.0.1:8080/page")
        self.assertFalse(safe)
        self.assertTrue(reason)

    def test_blocks_restricted_hostname(self):
        safe, reason = is_ssrf_safe_url("http://host.docker.internal/secret")
        self.assertFalse(safe)
        self.assertIn("restricted", reason)

    def test_blocks_metadata_host(self):
        safe, reason = is_ssrf_safe_url(
            "http://169.254.169.254/latest/meta-data/iam/security-credentials/"
        )
        self.assertFalse(safe)
        self.assertTrue(reason)

    def test_allows_public_https(self):
        safe, reason = is_ssrf_safe_url("https://example.com/article")
        self.assertTrue(safe, reason)
        self.assertEqual(reason, "")


if __name__ == "__main__":
    unittest.main()
