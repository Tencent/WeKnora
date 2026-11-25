import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

const PROC_ENTRY_GONE_CODES = new Set(["ENOENT", "ESRCH", "EACCES"]);

export function discoverBrowserEndpoints({
  sessionsDir = process.env.WK_BROWSER_SESSIONS_DIR ?? process.env.WK_SESSIONS_ROOT ?? "/var/lib/weknora/sessions",
  sharedRoot = process.env.WK_BROWSER_SHARED_ROOT ?? "/var/lib/weknora/browser/session",
  procRoot = process.env.WK_BROWSER_PROC_ROOT ?? "/proc",
  onUnreadable = () => {},
  includeLaunching = false,
} = {}) {
  const roots = new Set([resolve(sharedRoot)]);
  try {
    for (const name of readdirSync(sessionsDir)) {
      if (name.endsWith(".browser")) roots.add(resolve(sessionsDir, name));
    }
  } catch (error) {
    if (error.code !== "ENOENT") onUnreadable(error);
  }
  const live = new Map();
  let pids;
  try { pids = readdirSync(procRoot); } catch (error) {
    if (error.code !== "ENOENT") onUnreadable(error);
    return [];
  }
  for (const pid of pids) {
    if (!/^\d+$/.test(pid)) continue;
    try {
      const args = readFileSync(join(procRoot, pid, "cmdline"), "utf8").split("\0");
      if (!/(?:^|\/)(?:chrome|google-chrome|chromium)(?:[-.][^/]*)?$/.test(args[0]) ||
          args.some((arg) => arg.startsWith("--type="))) continue;
      const at = args.indexOf("--user-data-dir");
      const profile = args.find((arg) => arg.startsWith("--user-data-dir="))?.slice(16) ??
        (at >= 0 ? args[at + 1] : undefined);
      if (!profile?.startsWith("/")) continue;
      const root = dirname(resolve(profile));
      if (resolve(profile) !== join(root, "profile") || !roots.has(root) ||
          (!includeLaunching && existsSync(join(root, ".launching")))) continue;
      const endpoint = readBrowserRootEndpoint(root);
      live.set(root, { ...endpoint, pid: Number(pid) });
    } catch (error) {
      if (!PROC_ENTRY_GONE_CODES.has(error.code)) onUnreadable(error);
    }
  }
  const ports = new Set();
  return [...live.values()].sort((a, b) => a.root.localeCompare(b.root)).filter(({ port }) => {
    if (ports.has(port)) return false;
    ports.add(port);
    return true;
  });
}

export function readBrowserRootEndpoint(browserRoot) {
  const root = resolve(browserRoot);
  const profile = join(root, "profile");
  const lines = readFileSync(join(profile, "DevToolsActivePort"), "utf8").trim().split(/\r?\n/);
  const port = Number(lines[0]);
  if (!/^\d{1,5}$/.test(lines[0]) || port < 1 || port > 65535 ||
      !/^\/devtools\/browser\/[^/\s]+$/.test(lines[1] ?? "")) {
    throw Object.assign(new Error("invalid DevToolsActivePort"), { code: "EINVAL" });
  }
  return { root, profile, port, browserId: browserIdFromWsUrl(lines[1]) };
}

function log(message) {
  process.stderr.write(`cdp-cookies ${message}\n`);
}

function logUnlessTargetGone(step) {
  return (error) => {
    if (!isCdpTargetGone(error)) log(`${step} failed: ${error?.code ?? "unknown"}`);
  };
}

export async function getBrowserWsUrl(port, timeoutMs = WS_OPEN_TIMEOUT_MS) {
  const res = await fetch(`http://127.0.0.1:${port}/json/version`, {
    signal: AbortSignal.timeout(timeoutMs),
  });
  if (!res.ok) throw new Error(`/json/version HTTP ${res.status}`);
  const body = await res.json();
  const url = body.webSocketDebuggerUrl;
  if (typeof url !== "string" || url.length === 0) {
    throw new Error("no webSocketDebuggerUrl");
  }
  return url;
}

// Chrome mints the browser-target GUID in ws://<host>/devtools/browser/<GUID> when its DevTools server starts, once per browser process, so the GUID identifies one Chrome launch even on a reused CDP port (https://chromedevtools.github.io/devtools-protocol/#endpoints, https://chromium.googlesource.com/chromium/src/+/151.0.7922.169/content/browser/devtools/devtools_http_handler.cc).
export function browserIdFromWsUrl(wsUrl) {
  if (typeof wsUrl !== "string" || wsUrl.length === 0) return "";
  const match = /\/devtools\/browser\/([^/?#]+)/.exec(wsUrl);
  return match != null ? match[1] : wsUrl;
}

export function chromeVersionFromProduct(product) {
  const match = /Chrome\/(\d+(?:\.\d+){3})$/.exec(typeof product === "string" ? product : "");
  return match != null ? match[1] : "";
}

export async function getBrowserVersion(port) {
  const res = await fetch(`http://127.0.0.1:${port}/json/version`);
  if (!res.ok) throw new Error(`/json/version HTTP ${res.status}`);
  const body = await res.json();
  const version = chromeVersionFromProduct(body.Browser);
  if (version === "")
    throw new Error(`/json/version Browser not a Chrome version: ${body.Browser}`);
  return version;
}

export const CDP_ERROR_CODES = Object.freeze({
  connectionClosed: "connection_closed",
  sendTimeout: "send_timeout",
  openTimeout: "open_timeout",
  sessionNotFound: "session_not_found",
  protocolParse: "protocol_parse_error",
  protocolInvalidRequest: "protocol_invalid_request",
  protocolMethodNotFound: "protocol_method_not_found",
  protocolInvalidParams: "protocol_invalid_params",
  protocolInternal: "protocol_internal_error",
  protocolServer: "protocol_server_error",
  protocolUnknown: "protocol_unknown_code",
  protocolMalformed: "protocol_malformed_error",
});

const CDP_ERROR_CODE_VALUES = new Set(Object.values(CDP_ERROR_CODES));
const CDP_PROTOCOL_CODES = new Map([
  [-32700, CDP_ERROR_CODES.protocolParse],
  [-32600, CDP_ERROR_CODES.protocolInvalidRequest],
  [-32601, CDP_ERROR_CODES.protocolMethodNotFound],
  [-32602, CDP_ERROR_CODES.protocolInvalidParams],
  [-32603, CDP_ERROR_CODES.protocolInternal],
  [-32000, CDP_ERROR_CODES.protocolServer],
  [-32001, CDP_ERROR_CODES.sessionNotFound],
]);

export class CdpError extends Error {
  constructor(code, message) {
    super(message);
    if (!CDP_ERROR_CODE_VALUES.has(code)) throw new TypeError("Invalid CDP error code");
    Object.defineProperty(this, "code", { value: code, enumerable: true });
  }
}

export class CdpProtocolError extends CdpError {
  constructor(protocolCode, message) {
    const validCode = Number.isInteger(protocolCode);
    super(
      validCode
        ? (CDP_PROTOCOL_CODES.get(protocolCode) ?? CDP_ERROR_CODES.protocolUnknown)
        : CDP_ERROR_CODES.protocolMalformed,
      message,
    );
    this.protocolCode = validCode ? protocolCode : undefined;
  }
}

export function isCdpTargetGone(error) {
  return (
    error instanceof CdpError &&
    (error.code === CDP_ERROR_CODES.connectionClosed ||
      error.code === CDP_ERROR_CODES.sessionNotFound)
  );
}

export function isConnectionRefused(error) {
  return error?.cause?.code === "ECONNREFUSED";
}

export const CDP_SEND_TIMEOUT_MS = 15000;

function isSendTimeout(error) {
  return error instanceof CdpError && error.code === CDP_ERROR_CODES.sendTimeout;
}

function logDroppedFrame(error) {
  log(`dropped a CDP frame that is not JSON: ${error?.name ?? error?.code ?? "unknown"}`);
}

export class CdpBrowser {
  constructor(port, ws, browserId = "", onDroppedFrame = logDroppedFrame) {
    this.port = port;
    this.ws = ws;
    this.browserId = browserId;
    this.onDroppedFrame = onDroppedFrame;
    this.nextId = 1;
    this.pending = new Map();
    this.eventListeners = new Set();
    this.isClosed = false;
    ws.onmessage = (event) => this.onMessage(event);
    ws.onclose = () => this.fail(new CdpError(CDP_ERROR_CODES.connectionClosed, "socket closed"));
    ws.onerror = () => this.fail(new CdpError(CDP_ERROR_CODES.connectionClosed, "socket error"));
  }

  onMessage(event) {
    let msg;
    try {
      msg = JSON.parse(typeof event.data === "string" ? event.data : "");
    } catch (error) {
      this.onDroppedFrame(error);
      return;
    }
    if (msg.id === undefined) {
      for (const listener of this.eventListeners) listener(msg);
      return;
    }
    if (!this.pending.has(msg.id)) return;
    const { resolve, reject } = this.pending.get(msg.id);
    this.pending.delete(msg.id);
    if (msg.error) {
      reject(
        new CdpProtocolError(
          msg.error.code,
          typeof msg.error.message === "string" ? msg.error.message : "CDP error",
        ),
      );
    } else resolve(msg.result);
  }

  fail(error) {
    if (this.isClosed) return;
    this.isClosed = true;
    for (const { reject } of this.pending.values()) reject(error);
    this.pending.clear();
    this.ws.close();
  }

  close() {
    this.fail(new CdpError(CDP_ERROR_CODES.connectionClosed, "connection closed"));
  }

  onEvent(listener) {
    this.eventListeners.add(listener);
    return () => this.eventListeners.delete(listener);
  }

  send(method, params, sessionId) {
    if (this.isClosed) {
      return Promise.reject(new CdpError(CDP_ERROR_CODES.connectionClosed, "connection closed"));
    }
    const id = this.nextId++;
    const payload = { id, method, params: params ?? {} };
    if (sessionId != null) payload.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(
          new CdpError(
            CDP_ERROR_CODES.sendTimeout,
            `CDP ${method} timed out after ${CDP_SEND_TIMEOUT_MS}ms`,
          ),
        );
      }, CDP_SEND_TIMEOUT_MS);
      const settle = (finish) => (outcome) => {
        clearTimeout(timer);
        finish(outcome);
      };
      this.pending.set(id, { resolve: settle(resolve), reject: settle(reject) });
      try {
        this.ws.send(JSON.stringify(payload));
      } catch (error) {
        this.pending.delete(id);
        settle(reject)(error);
      }
    });
  }
}

export const WS_OPEN_TIMEOUT_MS = 10000;

export async function connectBrowser(port, onDroppedFrame = logDroppedFrame) {
  const wsUrl = await getBrowserWsUrl(port);
  const browserId = browserIdFromWsUrl(wsUrl);
  return await new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    const timer = setTimeout(() => {
      ws.close();
      reject(new CdpError(CDP_ERROR_CODES.openTimeout, "WebSocket open timed out"));
    }, WS_OPEN_TIMEOUT_MS);
    ws.onopen = () => {
      clearTimeout(timer);
      resolve(new CdpBrowser(port, ws, browserId, onDroppedFrame));
    };
    ws.onerror = () => {
      clearTimeout(timer);
      reject(new CdpError(CDP_ERROR_CODES.connectionClosed, "WebSocket connect failed"));
    };
  });
}

export function partitionKeyString(cookie) {
  const key = cookie.partitionKey;
  if (key == null) return "";
  return typeof key === "string" ? key : JSON.stringify(key);
}

export function cookieKey(cookie) {
  return [cookie.domain, cookie.path, cookie.name, partitionKeyString(cookie)].join("\u0000");
}

export function cookieFingerprint(cookie) {
  return [cookie.value, cookie.secure ? 1 : 0, cookie.httpOnly ? 1 : 0, cookie.sameSite ?? ""].join(
    "\u0000",
  );
}

export const ROTATING_AUTH_COOKIE_NAMES = new Set([
  "__Secure-1PSIDTS",
  "__Secure-3PSIDTS",
  "SIDCC",
  "__Secure-1PSIDCC",
  "__Secure-3PSIDCC",
]);

export function isRotatingAuthCookie(name) {
  return ROTATING_AUTH_COOKIE_NAMES.has(name);
}

export function cookieRecency(cookie) {
  return typeof cookie.expires === "number" && cookie.expires > 0 ? cookie.expires : 0;
}

const COOKIE_SAMESITE = new Set(["Strict", "Lax", "None"]);

function cookieHost(domain) {
  if (typeof domain !== "string" || domain.length === 0) return null;
  const host = domain.replace(/^\./, "");
  if (host.length === 0 || /[\0-\x20/@\\]/.test(host)) return null;
  return host;
}

function hostOnlyCookieUrl(domain, path, secure) {
  if (typeof path !== "string" || !path.startsWith("/")) return null;
  const host = cookieHost(domain);
  if (host == null) return null;
  const url = `${secure ? "https" : "http"}://${host}${path}`;
  try {
    const parsed = new URL(url);
    if (parsed.username !== "" || parsed.password !== "") return null;
    if (parsed.hostname.toLowerCase() !== host.toLowerCase()) return null;
    return url;
  } catch {
    return null;
  }
}

export function toCookieParam(cookie) {
  if (cookie == null || typeof cookie.name !== "string" || cookie.name.length === 0) {
    return null;
  }
  if (typeof cookie.path !== "string" || !cookie.path.startsWith("/")) return null;
  const hostPrefixed = cookie.name.startsWith("__Host-");
  const securePrefixed = cookie.name.startsWith("__Secure-");
  if ((hostPrefixed || securePrefixed) && cookie.secure !== true) return null;
  if (hostPrefixed && cookie.path !== "/") return null;
  if (cookie.sameSite != null && !COOKIE_SAMESITE.has(cookie.sameSite)) return null;
  if (cookie.sameSite === "None" && cookie.secure !== true) return null;
  const param = {
    name: cookie.name,
    value: cookie.value,
    path: cookie.path,
    secure: cookie.secure,
    httpOnly: cookie.httpOnly,
  };
  if (typeof cookie.domain !== "string" || cookie.domain.length === 0) return null;
  // CDP reports a host-only cookie without the leading dot. Passing that as
  // `domain` would store a domain cookie under a different key, so host-only
  // cookies are set through their URL instead.
  if (hostPrefixed || !cookie.domain.startsWith(".")) {
    const url = hostOnlyCookieUrl(cookie.domain, cookie.path, hostPrefixed || cookie.secure === true);
    if (url == null) return null;
    param.url = url;
  } else {
    param.domain = cookie.domain;
  }
  if (cookie.sameSite != null) param.sameSite = cookie.sameSite;
  if (cookie.session !== true && typeof cookie.expires === "number" && cookie.expires > 0) {
    param.expires = cookie.expires;
  }
  if (cookie.priority != null) param.priority = cookie.priority;
  if (cookie.partitionKey != null) param.partitionKey = cookie.partitionKey;
  return param;
}

export async function readCookies(browser) {
  const result = await browser.send("Storage.getCookies");
  const byKey = new Map();
  for (const cookie of result.cookies ?? []) byKey.set(cookieKey(cookie), cookie);
  return byKey;
}

function logRefusedCookies(browser, refused, total, error) {
  log(
    `the jar on CDP port ${browser.port} refused ${refused} of ${total} cookies: ${error?.code ?? "unknown"}`,
  );
}

export async function pushCookies(browser, cookieParams, onRefused = logRefusedCookies) {
  const params = cookieParams.filter((param) => param != null);
  if (params.length === 0) return;
  try {
    await browser.send("Storage.setCookies", { cookies: params });
  } catch (batchError) {
    let refused = 0;
    let lastError = batchError;
    for (const param of params) {
      await browser.send("Storage.setCookies", { cookies: [param] }).catch((error) => {
        refused += 1;
        lastError = error;
      });
    }
    if (refused > 0 && !isCdpTargetGone(lastError)) {
      onRefused(browser, refused, params.length, lastError);
    }
  }
}

export async function listPageTargets(browser) {
  const result = await browser.send("Target.getTargets");
  const targets = [];
  for (const info of result.targetInfos ?? []) {
    if (info.type !== "page") continue;
    if (!/^https?:\/\//i.test(info.url ?? "")) continue;
    targets.push({ targetId: info.targetId, url: info.url });
  }
  return targets;
}

export async function attachToPage(browser, targetId) {
  const result = await browser
    .send("Target.attachToTarget", { targetId, flatten: true })
    .catch(logUnlessTargetGone("Target.attachToTarget"));
  return result?.sessionId ?? null;
}

export async function detachSession(browser, sessionId) {
  await browser
    .send("Target.detachFromTarget", { sessionId })
    .catch(logUnlessTargetGone("Target.detachFromTarget"));
}

async function evaluate(browser, sessionId, expression) {
  const result = await browser.send(
    "Runtime.evaluate",
    { expression, returnByValue: true },
    sessionId,
  );
  if (result?.exceptionDetails != null) return undefined;
  return result?.result?.value;
}

export async function readPageStorage(browser, sessionId) {
  const raw = await evaluate(
    browser,
    sessionId,
    `(() => { try {
       const items = {};
       for (let i = 0; i < localStorage.length; i++) {
         const k = localStorage.key(i);
         if (k != null) items[k] = localStorage.getItem(k);
       }
       return JSON.stringify({ origin: location.origin, items });
     } catch (_e) { return null; } })()`,
  ).catch((error) => {
    if (!isCdpTargetGone(error) && !isSendTimeout(error)) {
      log(`Runtime.evaluate for localStorage failed: ${error?.code ?? "unknown"}`);
    }
  });
  if (typeof raw !== "string") return null;
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (parsed == null || typeof parsed.origin !== "string") return null;
  return {
    origin: parsed.origin,
    items: new Map(Object.entries(parsed.items ?? {})),
  };
}

export async function setPageStorage(browser, sessionId, entries) {
  for (const [key, value] of entries) {
    const expr = `try { localStorage.setItem(${JSON.stringify(key)}, ${JSON.stringify(
      value,
    )}); } catch (_e) {}`;
    await browser
      .send("Runtime.evaluate", { expression: expr }, sessionId)
      .catch(logUnlessTargetGone("Runtime.evaluate for localStorage.setItem"));
  }
}

export async function reloadPage(browser, sessionId) {
  await browser
    .send("Runtime.evaluate", { expression: "location.reload()" }, sessionId)
    .catch(logUnlessTargetGone("Runtime.evaluate for location.reload"));
}

export function hostMatchesCookieDomain(host, cookieDomain) {
  if (typeof host !== "string" || typeof cookieDomain !== "string") return false;
  const h = host.toLowerCase();
  const d = cookieDomain.replace(/^\./, "").toLowerCase();
  if (d.length === 0) return false;
  return h === d || h.endsWith(`.${d}`);
}

export function hostOfOrigin(origin) {
  try {
    return new URL(origin).hostname.toLowerCase();
  } catch {
    return "";
  }
}
