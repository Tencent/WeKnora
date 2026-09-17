"""Per-browser forward proxy with DNS pinning at the final connection.

Unlike Playwright route hooks, a proxy also sees redirected connections. HTTPS
stays end-to-end encrypted; CONNECT only opens a socket to a checked numeric IP.
An operator-configured HTTP(S) upstream proxy receives that numeric IP too.
"""
from __future__ import annotations

import asyncio
import base64
import contextlib
import ipaddress
from urllib.parse import unquote, urlsplit

from docreader.utils.ssrf import (
    _is_restricted_ip, _is_whitelisted, _resolve_host_ips, is_ssrf_safe_url,
)


async def checked_address(url: str) -> tuple[str, int]:
    parsed = urlsplit(url)
    if parsed.scheme not in {"http", "https"} or parsed.username is not None:
        raise ValueError("unsupported proxy target")
    safe, reason = await asyncio.to_thread(is_ssrf_safe_url, url)
    if not safe:
        raise ValueError(reason)
    host = parsed.hostname or ""
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    # Resolve once for the socket, recheck all answers, then dial a numeric IP.
    ips, error = await asyncio.to_thread(_resolve_host_ips, host)
    if error or not ips:
        raise ValueError("target DNS lookup failed")
    if not _is_whitelisted(host):
        for ip in ips:
            if _is_restricted_ip(ip):
                raise ValueError("target resolved to a restricted address")
    return str(ips[0]), port


class SSRFProxy:
    def __init__(self, upstream: str | None = None):
        self.upstream = urlsplit(upstream) if upstream else None
        if self.upstream and self.upstream.scheme not in {"http", "https"}:
            raise ValueError("browser upstream proxy must use HTTP or HTTPS")
        self.tasks: set[asyncio.Task] = set()
        self.server = None
        self.url = ""

    async def __aenter__(self):
        self.server = await asyncio.start_server(self._accept, "127.0.0.1", 0, limit=65536)
        self.url = f"http://127.0.0.1:{self.server.sockets[0].getsockname()[1]}"
        return self

    async def __aexit__(self, *_):
        self.server.close()
        await self.server.wait_closed()
        for task in list(self.tasks):
            task.cancel()
        await asyncio.gather(*self.tasks, return_exceptions=True)

    def _accept(self, reader, writer):
        task = asyncio.create_task(self._handle(reader, writer))
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    async def _connect(self, ip: str, port: int):
        if not self.upstream:
            return await asyncio.wait_for(asyncio.open_connection(ip, port), 15)
        proxy = self.upstream
        reader, writer = await asyncio.wait_for(asyncio.open_connection(
            proxy.hostname, proxy.port or (443 if proxy.scheme == "https" else 80),
            ssl=True if proxy.scheme == "https" else None,
        ), 15)
        try:
            authority = f"[{ip}]:{port}" if ipaddress.ip_address(ip).version == 6 else f"{ip}:{port}"
            headers = f"CONNECT {authority} HTTP/1.1\r\nHost: {authority}\r\n"
            if proxy.username is not None:
                credentials = f"{unquote(proxy.username)}:{unquote(proxy.password or '')}"
                headers += "Proxy-Authorization: Basic " + base64.b64encode(credentials.encode()).decode() + "\r\n"
            writer.write((headers + "\r\n").encode())
            await writer.drain()
            response = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 15)
            if response.split(b" ", 2)[1] != b"200":
                raise ValueError("upstream proxy refused connection")
            return reader, writer
        except BaseException:
            writer.close()
            raise

    async def _handle(self, reader, writer):
        upstream_writer = None
        relays = []
        connected = False
        try:
            header = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), 15)
            lines = header.decode("latin-1").split("\r\n")
            method, target, version = lines[0].split(" ")
            if version not in {"HTTP/1.0", "HTTP/1.1"}:
                raise ValueError("unsupported HTTP version")
            url = "https://" + target if method == "CONNECT" else target
            parsed = urlsplit(url)
            if method == "CONNECT" and (parsed.path or parsed.query or parsed.fragment):
                raise ValueError("invalid CONNECT target")
            if method != "CONNECT" and parsed.scheme != "http":
                raise ValueError("HTTPS requires CONNECT")
            ip, port = await checked_address(url)
            upstream_reader, upstream_writer = await self._connect(ip, port)
            if method == "CONNECT":
                writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                await writer.drain()
            else:
                # One origin per connection. Never relay proxy credentials, an
                # attacker-selected Host, or a second proxy-form request.
                path = parsed.path or "/"
                if parsed.query:
                    path += "?" + parsed.query
                forwarded = [f"{method} {path} {version}", f"Host: {parsed.netloc}", "Connection: close"]
                for line in lines[1:]:
                    if not line:
                        continue
                    name, sep, _ = line.partition(":")
                    if not sep or name.strip() != name:
                        raise ValueError("invalid header")
                    if name.lower() not in {"host", "connection", "proxy-connection", "proxy-authorization"}:
                        forwarded.append(line)
                upstream_writer.write(("\r\n".join(forwarded) + "\r\n\r\n").encode("latin-1"))
                await upstream_writer.drain()
            connected = True

            async def relay(source, destination):
                while data := await asyncio.wait_for(source.read(65536), 60):
                    destination.write(data)
                    await destination.drain()

            relays = [asyncio.create_task(relay(reader, upstream_writer)), asyncio.create_task(relay(upstream_reader, writer))]
            done, _ = await asyncio.wait(relays, return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                task.result()
        except (ValueError, OSError, asyncio.TimeoutError, asyncio.IncompleteReadError, asyncio.LimitOverrunError):
            if not connected:
                with contextlib.suppress(OSError):
                    writer.write(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                    await writer.drain()
        finally:
            for task in relays:
                task.cancel()
            await asyncio.gather(*relays, return_exceptions=True)
            if upstream_writer:
                upstream_writer.close()
            writer.close()
            with contextlib.suppress(OSError):
                await writer.wait_closed()
