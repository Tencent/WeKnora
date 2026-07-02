"""SSRF URL validation for docreader outbound HTTP requests.

Mirrors the core policy in internal/utils/security.go so redirect targets
during Playwright navigation are blocked the same way as Go-side imports.
"""

from __future__ import annotations

import ipaddress
import os
import re
import socket
from functools import lru_cache
from typing import FrozenSet, Optional, Tuple, Union
from urllib.parse import urlparse

RESTRICTED_HOSTNAMES: FrozenSet[str] = frozenset(
    {
        "localhost",
        "127.0.0.1",
        "::1",
        "0.0.0.0",
        "metadata.google.internal",
        "metadata.tencentyun.com",
        "metadata.aws.internal",
        "host.docker.internal",
        "gateway.docker.internal",
        "kubernetes.docker.internal",
        "kubernetes",
        "kubernetes.default",
        "kubernetes.default.svc",
        "kubernetes.default.svc.cluster.local",
    }
)

RESTRICTED_SUFFIXES: Tuple[str, ...] = (
    ".local",
    ".localhost",
    ".internal",
    ".corp",
    ".lan",
    ".home",
    ".localdomain",
    ".svc.cluster.local",
    ".pod.cluster.local",
)

EXTRA_RESTRICTED_CIDRS: Tuple[Union[ipaddress.IPv4Network, ipaddress.IPv6Network], ...] = tuple(
    ipaddress.ip_network(cidr)
    for cidr in (
        "100.64.0.0/10",
        "198.18.0.0/15",
        "198.51.100.0/24",
        "203.0.113.0/24",
        "192.0.0.0/24",
        "192.0.2.0/24",
        "0.0.0.0/8",
        "240.0.0.0/4",
        "255.255.255.255/32",
        "172.17.0.0/16",
        "172.18.0.0/16",
        "172.19.0.0/16",
        "172.20.0.0/16",
    )
)

BLOCKED_PORTS: FrozenSet[str] = frozenset(
    {
        "22",
        "23",
        "25",
        "445",
        "3389",
        "5432",
        "3306",
        "6379",
        "27017",
        "9200",
        "2379",
        "2380",
        "8500",
        "4001",
    }
)

_IP_LIKE_PATTERNS = (
    re.compile(r"^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$"),
    re.compile(r"^\d{8,10}$"),
    re.compile(r"^0[0-7]+\."),
    re.compile(r"(?i)^0x[0-9a-f]+\."),
    re.compile(r"(?i)^0x[0-9a-f]{6,8}$"),
    re.compile(r"(?i)^[0-9a-f:]+::[0-9a-f:]*$"),
    re.compile(r"(?i)^[0-9a-f]{1,4}(:[0-9a-f]{1,4}){7}$"),
)


def _normalize_url(raw_url: str) -> str:
    if "://" not in raw_url:
        return f"https://{raw_url}"
    return raw_url


@lru_cache(maxsize=1)
def _load_whitelist() -> Tuple[FrozenSet[str], Tuple[str, ...], Tuple[Union[ipaddress.IPv4Network, ipaddress.IPv6Network], ...]]:
    entries: list[str] = []
    for env_key in ("SSRF_WHITELIST", "SSRF_WHITELIST_EXTRA"):
        raw = os.environ.get(env_key, "")
        if raw.strip():
            entries.extend(part.strip() for part in raw.split(",") if part.strip())

    exact_hosts: set[str] = set()
    suffix_hosts: list[str] = []
    cidr_nets: list[Union[ipaddress.IPv4Network, ipaddress.IPv6Network]] = []
    for entry in entries:
        lowered = entry.lower()
        if lowered.startswith("*."):
            suffix_hosts.append(lowered[1:])
            continue
        if "/" in lowered:
            try:
                cidr_nets.append(ipaddress.ip_network(lowered, strict=False))
            except ValueError:
                continue
            continue
        exact_hosts.add(lowered)
    return frozenset(exact_hosts), tuple(suffix_hosts), tuple(cidr_nets)


def _is_whitelisted(hostname: str) -> bool:
    lowered = hostname.lower()
    exact_hosts, suffix_hosts, cidr_nets = _load_whitelist()
    if lowered in exact_hosts:
        return True
    for suffix in suffix_hosts:
        if lowered.endswith(suffix) or lowered == suffix.lstrip("."):
            return True
    try:
        ip = ipaddress.ip_address(lowered)
    except ValueError:
        return False
    return any(ip in net for net in cidr_nets)


def _is_ip_like_hostname(hostname: str) -> bool:
    return any(pattern.search(hostname) for pattern in _IP_LIKE_PATTERNS)


def _is_restricted_ip(ip: Union[ipaddress.IPv4Address, ipaddress.IPv6Address]) -> Optional[str]:
    if ip.is_private:
        return "private IP address"
    if ip.is_loopback:
        return "loopback address"
    if ip.is_link_local:
        return "link-local address"
    if ip.is_multicast:
        return "multicast address"
    if ip.is_unspecified:
        return "unspecified address"
    if isinstance(ip, ipaddress.IPv4Address):
        for net in EXTRA_RESTRICTED_CIDRS:
            if isinstance(net, ipaddress.IPv4Network) and ip in net:
                return f"restricted range {net}"
    if isinstance(ip, ipaddress.IPv6Address):
        # Site-local (fec0::/10)
        if (ip.packed[0] == 0xFE) and (ip.packed[1] & 0xC0) == 0xC0:
            return "site-local IPv6 address"
    return None


def _resolve_host_ips(hostname: str) -> Tuple[Tuple[Union[ipaddress.IPv4Address, ipaddress.IPv6Address], ...], Optional[str]]:
    try:
        infos = socket.getaddrinfo(hostname, None, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        return (), f"DNS resolution failed for hostname {hostname}: {exc}"
    ips: list[Union[ipaddress.IPv4Address, ipaddress.IPv6Address]] = []
    seen: set[str] = set()
    for info in infos:
        sockaddr = info[4]
        if not sockaddr:
            continue
        ip_str = sockaddr[0]
        if ip_str in seen:
            continue
        seen.add(ip_str)
        try:
            ips.append(ipaddress.ip_address(ip_str))
        except ValueError:
            continue
    if not ips:
        return (), f"DNS resolution failed for hostname {hostname}: no addresses"
    return tuple(ips), None


def is_ssrf_safe_url(raw_url: str) -> Tuple[bool, str]:
    """Return (safe, reason). reason is empty when safe is True."""
    if not raw_url or not raw_url.strip():
        return False, "URL is empty"

    normalized = _normalize_url(raw_url.strip())
    parsed = urlparse(normalized)
    scheme = (parsed.scheme or "").lower()
    if scheme not in {"http", "https"}:
        return False, f"invalid scheme: {scheme or '(none)'} (only http/https allowed)"

    hostname = (parsed.hostname or "").strip()
    if not hostname:
        return False, "URL has no hostname"

    hostname_lower = hostname.lower()
    if _is_whitelisted(hostname_lower):
        return True, ""

    if hostname_lower in RESTRICTED_HOSTNAMES:
        return False, f"hostname {hostname_lower} is restricted"

    for suffix in RESTRICTED_SUFFIXES:
        if hostname_lower.endswith(suffix):
            return False, f"hostname suffix {suffix} is restricted"

    try:
        ipaddress.ip_address(hostname_lower)
        return False, "direct IP address access is not allowed, use domain name or add to SSRF_WHITELIST"
    except ValueError:
        pass

    if _is_ip_like_hostname(hostname_lower):
        return False, "IP-like hostname format is not allowed"

    resolved_ips, resolve_err = _resolve_host_ips(hostname_lower)
    if resolve_err:
        return False, resolve_err

    for resolved_ip in resolved_ips:
        reason = _is_restricted_ip(resolved_ip)
        if reason:
            return (
                False,
                f"hostname {hostname_lower} resolves to restricted IP {resolved_ip}: {reason}",
            )

    port = parsed.port
    if port is not None and str(port) in BLOCKED_PORTS:
        return False, f"port {port} is blocked for security reasons"

    return True, ""


def reset_ssrf_whitelist_cache_for_test() -> None:
    """Clear cached whitelist entries (for unit tests only)."""
    _load_whitelist.cache_clear()
