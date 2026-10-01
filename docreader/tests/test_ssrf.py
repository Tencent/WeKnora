import ipaddress
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

    def _whitelist_only(self, whitelist: str, only: str) -> None:
        patcher = patch.dict(
            os.environ,
            {"SSRF_WHITELIST": whitelist, "SSRF_WHITELIST_EXTRA": "", "SSRF_DNS_WHITELIST_ONLY": only},
            clear=False,
        )
        patcher.start()
        self.addCleanup(patcher.stop)
        reset_ssrf_whitelist_cache_for_test()
        self.addCleanup(reset_ssrf_whitelist_cache_for_test)

    def test_whitelist_only_refuses_before_dns(self):
        self._whitelist_only("docs.internal", "true")
        safe, reason = is_ssrf_safe_url("https://outside.invalid/page")
        self.assertFalse(safe)
        self.assertIn("not in the SSRF whitelist", reason)
        self.assertNotIn("DNS resolution failed", reason)

    def test_whitelist_only_admits_whitelisted_host(self):
        self._whitelist_only("docs.internal", "true")
        safe, reason = is_ssrf_safe_url("https://docs.internal/page")
        self.assertTrue(safe, reason)

    def test_whitelist_only_off_leaves_validation_alone(self):
        self._whitelist_only("docs.internal", "false")
        safe, reason = is_ssrf_safe_url("http://127.0.0.1:8080/page")
        self.assertFalse(safe)
        self.assertNotIn("not in the SSRF whitelist", reason)

    def test_whitelist_only_unparsable_value_enables_it(self):
        self._whitelist_only("docs.internal", "yes")
        safe, reason = is_ssrf_safe_url("https://outside.invalid/page")
        self.assertFalse(safe)
        self.assertIn("not in the SSRF whitelist", reason)

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

    def test_blocks_invalid_port(self):
        safe, reason = is_ssrf_safe_url("https://example.com:99999/path")
        self.assertFalse(safe)
        self.assertIn("invalid port", reason)

    def test_blocks_ipv4_mapped_private_ipv6_resolution(self):
        with patch(
            "docreader.utils.ssrf._resolve_host_ips",
            return_value=((ipaddress.ip_address("::ffff:127.0.0.1"),), None),
        ):
            safe, reason = is_ssrf_safe_url("https://example.invalid/path")
        self.assertFalse(safe)
        self.assertIn("restricted", reason)

    def test_blocks_local_use_nat64(self):
        for address in ('64:ff9b:1::', '64:ff9b:1::a9fe:a9fe',
                        '64:ff9b:1::808:808', '64:ff9b:1:ffff:ffff:ffff:ffff:ffff'):
            with self.subTest(address=address), patch(
                'docreader.utils.ssrf._resolve_host_ips',
                return_value=((ipaddress.ip_address(address),), None),
            ):
                safe, reason = is_ssrf_safe_url('https://example.invalid/path')
                self.assertFalse(safe)
                self.assertIn('restricted', reason)

    def test_allows_public_https(self):
        safe, reason = is_ssrf_safe_url("https://example.com/article")
        self.assertTrue(safe, reason)
        self.assertEqual(reason, "")


if __name__ == "__main__":
    unittest.main()
