import asyncio
import ipaddress
import os
import unittest
from unittest.mock import patch

from docreader.utils.ssrf import reset_ssrf_whitelist_cache_for_test
from docreader.utils.ssrf_proxy import SSRFProxy, checked_address


class TestSSRFProxy(unittest.IsolatedAsyncioTestCase):
    async def test_dns_rebinding_is_checked_before_connect(self):
        for address in (
            '127.0.0.1', '64:ff9b:1::a9fe:a9fe', '64:ff9b:1::808:808',
            '64:ff9b:1:ffff:ffff:ffff:ffff:ffff',
        ):
            with self.subTest(address=address), \
                 patch('docreader.utils.ssrf_proxy.is_ssrf_safe_url', return_value=(True, '')), \
                 patch('docreader.utils.ssrf_proxy._resolve_host_ips', return_value=((ipaddress.ip_address(address),), None)), \
                 patch('docreader.utils.ssrf_proxy._is_whitelisted', return_value=False):
                with self.assertRaises(ValueError):
                    await checked_address('https://rebind.example/')

    async def test_proxy_refuses_restricted_connect(self):
        with patch.dict(os.environ, {'SSRF_WHITELIST': '', 'SSRF_WHITELIST_EXTRA': ''}):
            reset_ssrf_whitelist_cache_for_test()
            async with SSRFProxy() as proxy:
                port = int(proxy.url.rsplit(':', 1)[1])
                reader, writer = await asyncio.open_connection('127.0.0.1', port)
                writer.write(b'CONNECT 127.0.0.1:443 HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n')
                await writer.drain()
                self.assertIn(b'403 Forbidden', await reader.read())
                writer.close()
                await writer.wait_closed()
            reset_ssrf_whitelist_cache_for_test()

    async def test_upstream_proxy_receives_numeric_connect(self):
        seen = []

        async def upstream(reader, writer):
            try:
                seen.append(await reader.readuntil(b'\r\n\r\n'))
                writer.write(b'HTTP/1.1 200 Connection Established\r\n\r\n')
                await writer.drain()
            finally:
                writer.close()

        server = await asyncio.start_server(upstream, '127.0.0.1', 0)
        port = server.sockets[0].getsockname()[1]
        try:
            proxy = SSRFProxy(f'http://127.0.0.1:{port}')
            reader, writer = await proxy._connect('8.8.8.8', 443)
            self.assertIn(b'CONNECT 8.8.8.8:443 HTTP/1.1', seen[0])
            writer.close()
            await writer.wait_closed()
        finally:
            server.close()
            await server.wait_closed()

    async def test_webkit_redirects_and_subresources_use_proxy(self):
        try:
            from playwright.async_api import async_playwright
        except ImportError:
            self.skipTest('Playwright is required for browser integration test')
        secret_hits = []
        connections = set()

        async def serve(reader, writer):
            connections.add(writer)
            try:
                head = await reader.readuntil(b'\r\n\r\n')
                path = head.split(b' ')[1]
                if path == b'/blocked':
                    location = f'http://localhost:{port}/secret'.encode()
                    reply = b'HTTP/1.1 302 Found\r\nLocation: ' + location + b'\r\nContent-Length: 0\r\nConnection: close\r\n\r\n'
                elif path == b'/redirect':
                    reply = b'HTTP/1.1 302 Found\r\nLocation: /ok\r\nContent-Length: 0\r\nConnection: close\r\n\r\n'
                else:
                    if path == b'/secret':
                        secret_hits.append(path)
                    content = f'<html>public content<img src="http://localhost:{port}/secret"></html>'.encode()
                    reply = b'HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nConnection: close\r\nContent-Length: ' + str(len(content)).encode() + b'\r\n\r\n' + content
                writer.write(reply)
                await writer.drain()
            finally:
                writer.close()
                connections.discard(writer)

        server = await asyncio.start_server(serve, '127.0.0.1', 0)
        port = server.sockets[0].getsockname()[1]
        try:
            with patch.dict(os.environ, {'SSRF_WHITELIST': '127.0.0.1', 'SSRF_WHITELIST_EXTRA': ''}):
                reset_ssrf_whitelist_cache_for_test()
                async with SSRFProxy() as proxy, async_playwright() as p:
                    browser = await p.webkit.launch(proxy={'server': proxy.url, 'bypass': ''})
                    try:
                        page = await browser.new_page(service_workers='block')
                        # Intentionally no page.route: the proxy must cover hops
                        # that Playwright's route hook never observes.
                        await page.goto(f'http://127.0.0.1:{port}/redirect', wait_until='load')
                        self.assertTrue(page.url.endswith('/ok'))
                        self.assertIn('public content', await page.content())
                        try:
                            await page.goto(f'http://127.0.0.1:{port}/blocked', wait_until='load')
                        except Exception:
                            pass  # WebKit may surface a proxy refusal as navigation failure.
                        self.assertEqual(secret_hits, [])
                    finally:
                        await browser.close()
        finally:
            server.close()
            await server.wait_closed()
            for writer in connections:
                writer.close()
            reset_ssrf_whitelist_cache_for_test()
