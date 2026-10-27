import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { discoverBrowserEndpoints } from "../cdp-cookies.mjs";
import { buildUserAgentOverride } from "../wk-fingerprint-profiles.mjs";
import { chromeProxyServerForProfile, readBoxTimeZone } from "../wk-egress-timezone.mjs";
import {
  GOVERNED_MARKER, TZ_ATTACH_WAIT_MS, governBrowser, startTimeZone, tick, tickTimeZone,
} from "../wk-ua-governor.mjs";

function temporary(t) {
  const dir = fs.mkdtempSync(join(tmpdir(), "wk-gov-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function fakeBrowser(port = 1, browserId = "b1") {
  const listeners = new Set();
  const calls = [];
  return {
    port, browserId, calls, isClosed: false, chromeVersion: "142.0.7444.59", profile: "/p/profile",
    close() { this.isClosed = true; },
    onEvent(listener) { listeners.add(listener); return () => listeners.delete(listener); },
    emit(message) { for (const listener of listeners) listener(message); },
    async send(method, params, sessionId) { calls.push({ method, params, sessionId }); return {}; },
  };
}
const attach = (sessionId, type = "page") => ({
  method: "Target.attachedToTarget", params: { sessionId, targetInfo: { type }, waitingForDebugger: true },
});
const settle = () => new Promise((r) => setImmediate(r));
const sent = (b, method) => b.calls.filter((c) => c.method === method);

test("user agent is a Linux desktop Chrome at the running browser's major version", () => {
  const o = buildUserAgentOverride("142.0.7444.59");
  assert.match(o.userAgent, /^Mozilla\/5\.0 \(X11; Linux x86_64\).* Chrome\/142\.0\.0\.0 Safari\/537\.36$/);
  assert.equal(o.acceptLanguage, "en-US,en");
  assert.equal(o.platform, "Linux x86_64");
  const meta = o.userAgentMetadata;
  assert.equal(meta.platform, "Linux");
  assert.equal(meta.mobile, false);
  assert.equal(meta.bitness, "64");
  assert.ok(meta.brands.some((b) => b.brand === "Google Chrome" && b.version === "142"));
  assert.ok(meta.fullVersionList.some((b) => b.brand === "Chromium" && b.version === "142.0.7444.59"));
  assert.equal(new Set(meta.brands.map((b) => b.brand)).size, 3);
});

test("the claimed CPU is the one the browser runs on", () => {
  const arm = buildUserAgentOverride("142.0.7444.59", "arm64");
  assert.match(arm.userAgent, /^Mozilla\/5\.0 \(X11; Linux aarch64\)/);
  assert.equal(arm.platform, "Linux aarch64");
  assert.equal(arm.userAgentMetadata.architecture, "arm");
  assert.equal(arm.userAgentMetadata.bitness, "64");
  const x64 = buildUserAgentOverride("142.0.7444.59", "x64");
  assert.match(x64.userAgent, /\(X11; Linux x86_64\)/);
  assert.equal(x64.userAgentMetadata.architecture, "x86");
  assert.equal(buildUserAgentOverride("142.0.7444.59", "riscv64").platform, "Linux x86_64");
});

test("a new page gets UA, 8 cores and the resolved zone before it is resumed", async () => {
  const browser = fakeBrowser();
  await governBrowser(browser, { resolveZone: async () => ({ timeZone: "America/New_York", provider: "t", source: "exit_geo" }),
    boxZone: () => "Asia/Shanghai" });
  assert.equal(browser.calls[0].method, "Target.setAutoAttach");
  await settle();
  browser.emit(attach("s1"));
  await settle(); await settle();
  assert.equal(sent(browser, "Emulation.setUserAgentOverride")[0].sessionId, "s1");
  assert.equal(sent(browser, "Emulation.setHardwareConcurrencyOverride")[0].params.hardwareConcurrency, 8);
  assert.equal(sent(browser, "Emulation.setTimezoneOverride")[0].params.timezoneId, "America/New_York");
  const order = browser.calls.map((c) => c.method);
  assert.ok(order.indexOf("Runtime.runIfWaitingForDebugger") >= 0);
  assert.equal(sent(browser, "Target.setAutoAttach").length, 2, "the page attaches its own frames and workers");
});

test("a failed geo lookup falls back to the box zone, and a later answer replaces it", async () => {
  const browser = fakeBrowser();
  let lookups = 0;
  let clock = 0;
  const now = () => clock;
  const resolveZone = async () => {
    lookups += 1;
    if (lookups === 1) throw Object.assign(new Error("x"), { code: "no_provider(ipinfo=timeout)" });
    return { timeZone: "Europe/Berlin", provider: "t", source: "exit_geo" };
  };
  browser.attachedSessions = new Set(["s1"]);
  await startTimeZone(browser, { resolveZone, boxZone: () => "Asia/Shanghai", now });
  assert.equal(browser.tz.zone, "Asia/Shanghai", "fallback while the lookup is unresolved");
  assert.equal(browser.tz.hold, null, "page start is not held any longer");
  assert.equal(browser.tz.resolved, false);
  clock += 5_000;
  await tickTimeZone(browser, { now });
  assert.equal(browser.tz.zone, "Europe/Berlin");
  assert.equal(browser.tz.resolved, true);
  await settle();
  assert.equal(sent(browser, "Emulation.setTimezoneOverride").at(-1).params.timezoneId, "Europe/Berlin");
});

test("a UTC box clock is no fallback: the zone stays unset", async () => {
  const browser = fakeBrowser();
  browser.attachedSessions = new Set();
  await startTimeZone(browser, { resolveZone: async () => { throw new Error("offline"); }, boxZone: () => "Etc/UTC" });
  assert.equal(browser.tz.zone, null);
});

test("a slow lookup holds the page start only for the attach window", async () => {
  const browser = fakeBrowser();
  let clock = 0;
  let finish;
  const resolveZone = () => new Promise((resolve) => { finish = resolve; });
  browser.attachedSessions = new Set();
  startTimeZone(browser, { resolveZone, boxZone: () => "Asia/Shanghai", now: () => clock });
  assert.ok(browser.tz.hold != null);
  clock += TZ_ATTACH_WAIT_MS;
  tickTimeZone(browser, { now: () => clock });
  assert.equal(browser.tz.hold, null);
  assert.equal(browser.tz.zone, "Asia/Shanghai");
  finish({ timeZone: "Asia/Tokyo", provider: "t", source: "exit_geo" });
  await browser.tz.inFlight;
  assert.equal(browser.tz.zone, "Asia/Tokyo");
});

test("workers are governed but a service worker attaches no children", async () => {
  const browser = fakeBrowser();
  await governBrowser(browser, { resolveZone: async () => ({ timeZone: "Asia/Tokyo", provider: "t", source: "x" }), boxZone: () => null });
  await settle();
  browser.emit(attach("w1", "worker"));
  browser.emit(attach("sw1", "service_worker"));
  await settle(); await settle();
  const uaSessions = sent(browser, "Emulation.setUserAgentOverride").map((c) => c.sessionId).sort();
  assert.deepEqual(uaSessions, ["sw1", "w1"]);
  const children = sent(browser, "Target.setAutoAttach").map((c) => c.sessionId).filter(Boolean);
  assert.deepEqual(children, ["w1"]);
});

test("tick governs a launching browser, marks it, and clears the mark when it goes", async (t) => {
  const root = temporary(t);
  const browsers = new Map();
  const browser = fakeBrowser(7001, "boot");
  let endpoints = [{ root, profile: join(root, "profile"), port: 7001, browserId: "boot" }];
  const deps = {
    discover: () => endpoints, connect: async () => browser, version: async () => "142.0.7444.59",
    govern: async () => {},
  };
  await tick(browsers, deps);
  assert.equal(fs.readFileSync(join(root, GOVERNED_MARKER), "utf8").trim(), "boot");
  await tick(browsers, deps);
  assert.equal(browsers.size, 1, "an already governed browser is left alone");
  endpoints = [];
  await tick(browsers, deps);
  assert.equal(browsers.size, 0);
  assert.ok(!fs.existsSync(join(root, GOVERNED_MARKER)));
});

test("discovery sees a launching browser only when asked", (t) => {
  const dir = temporary(t);
  const root = join(dir, "s1.browser");
  const proc = join(dir, "proc");
  fs.mkdirSync(join(root, "profile"), { recursive: true });
  fs.mkdirSync(join(proc, "42"), { recursive: true });
  fs.writeFileSync(join(root, "profile", "DevToolsActivePort"), "9333\n/devtools/browser/abc\n");
  fs.writeFileSync(join(root, ".launching"), "");
  fs.writeFileSync(join(proc, "42", "cmdline"), ["/opt/google/chrome/chrome", `--user-data-dir=${root}/profile`, ""].join("\0"));
  const options = { sessionsDir: dir, sharedRoot: join(dir, "none"), procRoot: proc };
  assert.equal(discoverBrowserEndpoints(options).length, 0);
  assert.equal(discoverBrowserEndpoints({ ...options, includeLaunching: true })[0].port, 9333);
});

test("proxy is found by profile, and the box zone reads /etc/timezone when localtime is a file", (t) => {
  const dir = temporary(t);
  fs.mkdirSync(join(dir, "7"));
  fs.writeFileSync(join(dir, "7", "cmdline"),
    ["/chrome", "--user-data-dir=/s/a.browser/profile", "--proxy-server=http://10.0.0.2:8080", ""].join("\0"));
  assert.equal(chromeProxyServerForProfile("/s/a.browser/profile", { procRoot: dir }), "http://10.0.0.2:8080");
  assert.equal(chromeProxyServerForProfile("/s/b.browser/profile", { procRoot: dir }), undefined);
  const einval = () => { throw Object.assign(new Error("x"), { code: "EINVAL" }); };
  assert.equal(readBoxTimeZone({ readLink: einval, readFile: () => "Asia/Shanghai\n" }), "Asia/Shanghai");
  assert.equal(readBoxTimeZone({ readLink: () => "/usr/share/zoneinfo/Europe/Paris" }), "Europe/Paris");
});
