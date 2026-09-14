#!/usr/bin/env python3
"""Exercise the production embed Nginx locations with a policy backend fixture.

Requires Docker. Run: python3 scripts/test_embed_nginx.py [nginx-or-ui-image]
"""
import json
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
IMAGE = sys.argv[1] if len(sys.argv) > 1 else "nginx:alpine"


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def check():
    with tempfile.TemporaryDirectory(prefix="weknora-embed-nginx-") as folder:
        work = Path(folder)
        (work / "web").mkdir()
        (work / "web/embed.html").write_text("embed entry")
        (work / "web/index.html").write_text("main SPA")
        (work / "web/50x.html").write_text("backend unavailable")
        config = (ROOT / "frontend/nginx.conf").read_text()
        for key, value in {
            "APP_SCHEME": "http", "APP_HOST": "127.0.0.1", "APP_PORT": "18081",
            "MAX_FILE_SIZE": "50m", "MAX_SKILL_BUNDLE_SIZE": "100m",
        }.items():
            config = config.replace("${" + key + "}", value)
        config = config.replace("/usr/share/nginx/html", "/check/web")
        config = config.replace("/etc/nginx/api-proxy.conf", "/check/api-proxy.conf")
        (work / "api-proxy.conf").write_text((ROOT / "frontend/nginx-api-proxy.conf").read_text())
        # The real policy handler and token middleware are covered by Go tests.
        # Here vary its responses to verify subrequests, fallback and fail-closed serving.
        fixture = """
        server {
            listen 18081;
            location = /api/v1/embed-frame-policy {
                set $policy "frame-ancestors 'none'";
                if ($http_x_embed_page_uri ~ "^/embed/active(?:[?]|$)") {
                    set $policy "frame-ancestors 'self' https://a.example";
                }
                add_header Content-Security-Policy $policy always;
                if ($http_x_embed_page_uri = "/embed/unavailable") { return 500; }
                if ($policy = "frame-ancestors 'none'") { return 403; }
                return 204;
            }
            location /api/ { return 200 "$http_host"; }
        }
        """
        (work / "nginx.conf").write_text(
            "events {}\nhttp {\n" + (ROOT / "frontend/nginx-http.conf").read_text() + config + fixture + "\n}"
        )
        # The Nginx worker runs as an unprivileged user in the container.
        work.chmod(0o755)
        cid = docker("create", "-p", "127.0.0.1::80",
                     "--entrypoint", "nginx", IMAGE, "-c", "/check/nginx.conf", "-g", "daemon off;")
        try:
            docker("cp", str(work), cid + ":/check")
            docker("start", cid)
            info = json.loads(docker("inspect", cid))[0]
            port = info["NetworkSettings"]["Ports"]["80/tcp"][0]["HostPort"]
            base = f"http://127.0.0.1:{port}"

            def request(path, method="GET"):
                req = urllib.request.Request(base + path, method=method)
                try:
                    return urllib.request.urlopen(req, timeout=3)
                except urllib.error.HTTPError as error:
                    return error

            for attempt in range(30):
                try:
                    with request("/") as response:
                        assert response.status == 200
                    break
                except (urllib.error.URLError, ConnectionError):
                    if attempt == 29:
                        raise
                    time.sleep(0.1)
            for method in ("GET", "HEAD"):
                for path in ("/embed/active", "/embed/active?token=ignored"):
                    with request(path, method) as response:
                        assert response.status == 200, (path, response.status)
                        assert response.headers["Content-Security-Policy"] == "frame-ancestors 'self' https://a.example"
                        assert response.headers["Cache-Control"] == "no-store"
                        assert "X-Frame-Options" not in response.headers
                        assert response.read() == (b"embed entry" if method == "GET" else b"")
            for path, status in (("/embed/disabled", 403), ("/embed/empty", 403),
                                 ("/embed/unavailable", 500), ("/_embed-frame-policy", 404),
                                 ("/embed.html", 404)):
                with request(path) as response:
                    assert response.status == status, (path, response.status)
                    assert b"embed entry" not in response.read()
            with request("/api/probe") as response:
                assert response.read().decode() == f"127.0.0.1:{port}", "proxy lost public port"
            print("Embed Nginx integration checks passed")
        except Exception:
            print(docker("logs", cid), file=sys.stderr)
            raise
        finally:
            docker("rm", "-f", cid)


if __name__ == "__main__":
    check()
