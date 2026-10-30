import { readdirSync, readFileSync, readlinkSync } from "node:fs";
import { request as httpRequest } from "node:http";
import { connect as tlsConnect } from "node:tls";

import {
  isKnownTimeZone,
  isUtcTimeZone,
  parseZoneTab,
  resolveTimeZone,
} from "./wk-locale-match.mjs";

const COUNTRY_CODE_RE = /^[A-Z]{2}$/;
const REGION_CODE_RE = /^[A-Z0-9]{1,3}$/;

function exitGeoRecord({ country, region, timeZone }) {
  return {
    country: typeof country === "string" && COUNTRY_CODE_RE.test(country) ? country : undefined,
    region: typeof region === "string" && REGION_CODE_RE.test(region) ? region : undefined,
    timeZone: isKnownTimeZone(timeZone) ? timeZone : undefined,
  };
}

export const EGRESS_GEO_PROVIDERS = Object.freeze([
  Object.freeze({
    name: "ipinfo",
    host: "ipinfo.io",
    path: "/json",
    exitGeoOf: (body) => exitGeoRecord({ country: body?.country, timeZone: body?.timezone }),
  }),
  Object.freeze({
    name: "ipwho_is",
    host: "ipwho.is",
    path: "/",
    exitGeoOf: (body) =>
      exitGeoRecord(
        body?.success === false
          ? {}
          : {
              country: body?.country_code,
              region: body?.region_code,
              timeZone: body?.timezone?.id,
            },
      ),
  }),
]);

export const EGRESS_GEO_REQUEST_TIMEOUT_MS = 5_000;
const EGRESS_GEO_MAX_BODY_BYTES = 64 * 1024;
const EGRESS_GEO_USER_AGENT = "wk-egress-timezone/1";
const ZONEINFO_DIR = "zoneinfo/";
const EGRESS_ERROR_CLASS_PREFIXES = ["no_provider(", "no_time_zone("];
const PROC_ENTRY_GONE_CODES = new Set(["ENOENT", "ESRCH", "EACCES"]);

export class EgressGeoError extends Error {
  constructor(code, options) {
    super(`egress time zone lookup failed: ${code}`, options);
    this.name = "EgressGeoError";
    this.code = code;
  }
}

export function egressErrorCode(error) {
  if (typeof error?.code === "string") return error.code;
  if (typeof error?.cause?.code === "string") return error.cause.code;
  return error instanceof Error ? error.name : typeof error;
}

export function readBoxTimeZone({
  path = "/etc/localtime",
  namePath = "/etc/timezone",
  readLink = readlinkSync,
  readFile = readFileSync,
} = {}) {
  try {
    const target = readLink(path);
    const index = target.lastIndexOf(ZONEINFO_DIR);
    if (index !== -1) return target.slice(index + ZONEINFO_DIR.length);
  } catch (error) {
    if (error?.code !== "ENOENT" && error?.code !== "EINVAL") throw error;
  }
  try {
    return String(readFile(namePath, "utf8")).trim() || null;
  } catch (error) {
    if (error?.code === "ENOENT") return null;
    throw error;
  }
}

export function egressErrorClass(error) {
  const code = egressErrorCode(error);
  if (code === "browser_not_found" || code === "unsupported_proxy") return code;
  const prefix = EGRESS_ERROR_CLASS_PREFIXES.find((candidate) => code.startsWith(candidate));
  return prefix == null ? "other" : prefix.slice(0, -1);
}

export function isBoxClockUtc({ readLink = readlinkSync } = {}) {
  return isUtcTimeZone(readBoxTimeZone({ readLink }));
}

export function readZonesByCountry({
  path = "/usr/share/zoneinfo/zone.tab",
  readFile = readFileSync,
} = {}) {
  try {
    return parseZoneTab(readFile(path, "utf8"));
  } catch (error) {
    if (error?.code === "ENOENT") return new Map();
    throw error;
  }
}

export function chromeProxyServerForProfile(
  profile,
  { procRoot = "/proc", readDir = readdirSync, readFile = readFileSync } = {},
) {
  const profileArg = `--user-data-dir=${profile}`;
  for (const entry of readDir(procRoot)) {
    if (!/^\d+$/.test(entry)) continue;
    let cmdline;
    try {
      cmdline = readFile(`${procRoot}/${entry}/cmdline`, "utf8");
    } catch (error) {
      if (PROC_ENTRY_GONE_CODES.has(error?.code)) continue;
      throw error;
    }
    const args = String(cmdline).split("\0");
    if (!args.includes(profileArg) || args.some((arg) => arg.startsWith("--type="))) continue;
    const proxyArg = args.findLast((arg) => arg.startsWith("--proxy-server="));
    return proxyArg == null ? null : proxyArg.slice("--proxy-server=".length);
  }
  return undefined;
}

export function egressRouteFromProxyServer(proxyServer) {
  const value = proxyServer == null ? "" : proxyServer.trim();
  if (value === "" || value === "direct://") return { kind: "direct" };
  if (/[\s=;,]/.test(value)) return { kind: "unsupported" };
  let url;
  try {
    url = new URL(value.includes("://") ? value : `http://${value}`);
  } catch (error) {
    if (error instanceof TypeError) return { kind: "unsupported" };
    throw error;
  }
  if (
    url.protocol !== "http:" ||
    url.username !== "" ||
    url.password !== "" ||
    (url.pathname !== "" && url.pathname !== "/") ||
    url.search !== "" ||
    url.hash !== ""
  ) {
    return { kind: "unsupported" };
  }
  return {
    kind: "proxy",
    host: url.hostname.replace(/^\[(.*)\]$/, "$1"),
    port: url.port === "" ? 80 : Number(url.port),
  };
}

function openTunnel({ proxy, host, port, timeoutMs }) {
  return new Promise((resolve, reject) => {
    const authority = `${host}:${port}`;
    const request = httpRequest({
      host: proxy.host,
      port: proxy.port,
      method: "CONNECT",
      path: authority,
      headers: { host: authority },
      timeout: timeoutMs,
    });
    request.once("timeout", () => request.destroy(new EgressGeoError("connect_timeout")));
    request.once("connect", (response, socket) => {
      if (response.statusCode === 200) {
        resolve(socket);
        return;
      }
      socket.destroy();
      reject(new EgressGeoError(`connect_${response.statusCode}`));
    });
    request.once("error", reject);
    request.end();
  });
}

export async function requestJson({
  host,
  port = 443,
  path,
  proxy = null,
  timeoutMs = EGRESS_GEO_REQUEST_TIMEOUT_MS,
  secureConnect = tlsConnect,
}) {
  const tunnel = proxy == null ? null : await openTunnel({ proxy, host, port, timeoutMs });
  tunnel?.once("timeout", () => tunnel.destroy(new EgressGeoError("timeout")));
  return await new Promise((resolve, reject) => {
    const request = httpRequest(
      {
        host,
        port,
        path,
        method: "GET",
        headers: { host, accept: "application/json", "user-agent": EGRESS_GEO_USER_AGENT },
        timeout: timeoutMs,
        createConnection: () =>
          secureConnect(
            tunnel == null
              ? { host, port, servername: host, timeout: timeoutMs }
              : { socket: tunnel, servername: host },
          ),
      },
      (response) => {
        if (response.statusCode !== 200) {
          response.resume();
          reject(new EgressGeoError(`http_${response.statusCode}`));
          return;
        }
        const chunks = [];
        let size = 0;
        response.on("data", (chunk) => {
          size += chunk.length;
          if (size > EGRESS_GEO_MAX_BODY_BYTES) {
            request.destroy(new EgressGeoError("body_too_large"));
            return;
          }
          chunks.push(chunk);
        });
        response.on("end", () => {
          if (size > EGRESS_GEO_MAX_BODY_BYTES) return;
          try {
            resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
          } catch (error) {
            reject(new EgressGeoError("bad_json", { cause: error }));
          }
        });
        response.on("error", reject);
      },
    );
    request.once("timeout", () => request.destroy(new EgressGeoError("timeout")));
    request.on("error", (error) => {
      tunnel?.destroy();
      reject(error);
    });
    request.end();
  });
}

async function fetchExitGeo({ providers, request, proxy, timeoutMs }) {
  const failures = [];
  let partial = null;
  for (const provider of providers) {
    try {
      const body = await request({
        host: provider.host,
        port: provider.port,
        path: provider.path,
        proxy,
        timeoutMs,
      });
      const exitGeo = provider.exitGeoOf(body);
      if (exitGeo.timeZone != null) return { exitGeo, provider: provider.name };
      if (
        exitGeo.country != null &&
        (partial == null || (partial.exitGeo.region == null && exitGeo.region != null))
      ) {
        partial = { exitGeo, provider: provider.name };
      }
      failures.push(`${provider.name}=no_time_zone`);
    } catch (error) {
      failures.push(`${provider.name}=${egressErrorCode(error)}`);
    }
  }
  if (partial != null) return partial;
  throw new EgressGeoError(`no_provider(${failures.join(",")})`);
}

export async function resolveEgressTimeZone({
  profile,
  providers = EGRESS_GEO_PROVIDERS,
  proxyServerForProfile = chromeProxyServerForProfile,
  request = requestJson,
  timeoutMs = EGRESS_GEO_REQUEST_TIMEOUT_MS,
  boxTimeZone = readBoxTimeZone,
  zonesByCountry = readZonesByCountry,
}) {
  const proxyServer = proxyServerForProfile(profile);
  if (proxyServer === undefined) throw new EgressGeoError("browser_not_found");
  const route = egressRouteFromProxyServer(proxyServer);
  if (route.kind === "unsupported") throw new EgressGeoError("unsupported_proxy");
  const proxy = route.kind === "proxy" ? { host: route.host, port: route.port } : null;
  const { exitGeo, provider } = await fetchExitGeo({ providers, request, proxy, timeoutMs });
  const { timeZone, source } = resolveTimeZone({
    exitGeo,
    userTimeZone: boxTimeZone(),
    zonesByCountry: zonesByCountry(),
  });
  if (!isKnownTimeZone(timeZone)) throw new EgressGeoError(`no_time_zone(${provider})`);
  return { timeZone, provider, source };
}
