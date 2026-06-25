import json
import urllib.error
import urllib.request
import unittest

from docreader.main import start_health_server


class HealthCheckTest(unittest.TestCase):
    def setUp(self):
        self.server = start_health_server(0)
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()

    def test_healthz_returns_ok(self):
        with urllib.request.urlopen(f"{self.base_url}/healthz", timeout=2) as response:
            body = json.loads(response.read().decode("utf-8"))

        self.assertEqual(response.status, 200)
        self.assertEqual(body["status"], "ok")
        self.assertEqual(body["service"], "docreader")

    def test_unknown_path_returns_not_found(self):
        with self.assertRaises(urllib.error.HTTPError) as ctx:
            urllib.request.urlopen(f"{self.base_url}/unknown", timeout=2)

        self.assertEqual(ctx.exception.code, 404)


if __name__ == "__main__":
    unittest.main()
