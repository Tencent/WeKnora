import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { cookieKey } from "../cdp-cookies.mjs";
import { DAY_MS, isCredential, planSync, siteOf, viewEntry } from "../wk-cookie-plan.mjs";
import { BrowserSync, readSeed, ROOT_STATE_FILE, SEED_FILE } from "../wk-browser-sync.mjs";
import { ReloadBreaker, RELOAD_MAX_PER_WINDOW, RELOAD_QUIET_MS } from "../wk-page-sync.mjs";

const NOW = 1_800_000_000_000;
const YEAR = NOW / 1000 + 365 * 86400;

const google = (name, value, extra = {}) => ({
  name, value, domain: ".google.com", path: "/", secure: true, httpOnly: true, sameSite: "Lax",
  expires: YEAR, session: false, ...extra,
});
const site = (domain, name, value, extra = {}) => ({ ...google(name, value, extra), domain });
const jar = (cookies) => new Map(cookies.map((c) => [cookieKey(c), c]));

function temporary(t) {
  const dir = fs.mkdtempSync(join(tmpdir(), "wk-sync-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

let nextPort = 41000;
let nextBoot = 0;
function fakeBrowser({ cookies = [], pages = [] } = {}) {
  return {
    port: nextPort++,
    browserId: `boot-${nextBoot++}`,
    jar: jar(cookies),
    pages: pages.map(([origin, items]) => ({ origin, items: new Map(items) })),
    reloads: 0,
    refused: new Set(),
    isClosed: false,
    close() {},
    async send(method, params = {}, sessionId) {
      if (method === "Storage.getCookies") return { cookies: [...this.jar.values()].map((c) => ({ ...c })) };
      if (method === "Storage.setCookies") {
        for (const c of params.cookies) {
          if (this.refused.has(c.name)) throw new Error("refused");
          const landed = { ...c, domain: c.url ? new URL(c.url).hostname : c.domain };
          delete landed.url;
          landed.session = !(landed.expires > 0);
          if (landed.session) landed.expires = -1;
          this.jar.set(cookieKey(landed), landed);
        }
        return {};
      }
      if (method === "Target.getTargets") {
        return { targetInfos: this.pages.map((p, i) => ({ type: "page", targetId: String(i), url: `${p.origin}/` })) };
      }
      if (method === "Target.attachToTarget") return { sessionId: params.targetId };
      if (method === "Runtime.evaluate") {
        const page = this.pages[Number(sessionId)];
        if (params.expression.includes("localStorage.length")) {
          return { result: { value: JSON.stringify({ origin: page.origin, items: Object.fromEntries(page.items) }) } };
        }
        const set = /localStorage\.setItem\(("(?:[^"\\]|\\.)*"), ("(?:[^"\\]|\\.)*")\)/.exec(params.expression);
        if (set) page.items.set(JSON.parse(set[1]), JSON.parse(set[2]));
        if (params.expression === "location.reload()") this.reloads++;
        return { result: {} };
      }
      return {};
    },
  };
}

// A machine: session roots on disk, fake Chrome per root, one BrowserSync.
function machine(t) {
  const dir = temporary(t);
  const authDir = join(dir, "auth");
  const running = new Map();
  let clock = NOW;
  const make = () => new BrowserSync({
    authDir,
    now: () => clock,
    discover: () => [...running].map(([root, b]) => ({ root, port: b.port, browserId: b.browserId })),
    connect: async (port) => [...running.values()].find((b) => b.port === port),
  });
  const m = {
    sync: make(),
    authDir,
    advance(ms) { clock += ms; },
    restartDaemon() { m.sync = make(); },
    start(name, options) {
      const root = join(dir, `${name}.browser`);
      fs.mkdirSync(join(root, "profile"), { recursive: true });
      return m.run(root, fakeBrowser(options));
    },
    run(root, browser) {
      browser.root = root;
      fs.writeFileSync(join(root, "profile", "DevToolsActivePort"), `${browser.port}\n/devtools/browser/${browser.browserId}\n`);
      running.set(root, browser);
      return browser;
    },
    // Same profile, new Chrome process: persistent cookies survive.
    restart(browser) {
      return m.run(browser.root, fakeBrowser({ cookies: [...browser.jar.values()].filter((c) => !c.session) }));
    },
    stop(browser) { running.delete(browser.root); },
    seed() { return readSeed(fs, join(authDir, SEED_FILE)).cookies; },
    state(browser) { return JSON.parse(fs.readFileSync(join(browser.root, ROOT_STATE_FILE), "utf8")); },
    tick() { return m.sync.tick(); },
  };
  return m;
}

const value = (b, name, domain = ".google.com") =>
  [...b.jar.values()].find((c) => c.name === name && c.domain === domain)?.value;
const put = (b, c) => b.jar.set(cookieKey(c), c);
const drop = (b, name, domain = ".google.com") => {
  for (const [key, c] of b.jar) if (c.name === name && c.domain === domain) b.jar.delete(key);
};
const seedValue = (m, name, domain = ".google.com") =>
  m.seed().find((c) => c.name === name && c.domain === domain)?.value;

function logIn(b, account) {
  put(b, google("SID", `sid-${account}`, { httpOnly: false }));
  put(b, google("HSID", `hsid-${account}`));
  put(b, google("SSID", `ssid-${account}`));
}
function logOut(b) {
  for (const name of ["SID", "HSID", "SSID"]) drop(b, name);
  put(b, google("NID", "logged-out-nid", { httpOnly: false }));
}

test("siteOf groups subdomains under the registrable domain", () => {
  assert.equal(siteOf(".google.com"), "google.com");
  assert.equal(siteOf("mail.google.com"), "google.com");
  assert.equal(siteOf("accounts.google.com."), "google.com");
  assert.equal(siteOf("www.bbc.co.uk"), "bbc.co.uk");
  assert.equal(siteOf(".example.com.cn"), "example.com.cn");
  assert.equal(siteOf("a.b.example.de"), "example.de");
  assert.equal(siteOf("alice.github.io"), "alice.github.io");
  assert.equal(siteOf("127.0.0.1"), "127.0.0.1");
  assert.equal(siteOf("localhost"), "localhost");
});

test("credential cookies are httpOnly, non-rotating, session or at least a day long", () => {
  const now = NOW;
  assert.equal(isCredential("HSID", viewEntry(google("HSID", "x")), now), true);
  assert.equal(isCredential("SID", viewEntry(google("SID", "x", { httpOnly: false })), now), false);
  assert.equal(isCredential("sess", viewEntry(google("sess", "x", { session: true, expires: -1 })), now), true);
  assert.equal(isCredential("csrf", viewEntry(google("csrf", "x", { expires: now / 1000 + 3600 })), now), false);
  assert.equal(isCredential("__Secure-1PSIDTS", viewEntry(google("__Secure-1PSIDTS", "x")), now), false);
  assert.ok(DAY_MS > 0);
});

test("A logs in, B and C follow; C logs out alone and stops receiving; C logs in again and wins", async (t) => {
  const m = machine(t);
  const a = m.start("a");
  const b = m.start("b");
  let c = m.start("c", { cookies: [site(".example.org", "other", "o1")] });
  await m.tick();

  logIn(a, "one");
  await m.tick();
  for (const peer of [b, c]) {
    assert.equal(value(peer, "SID"), "sid-one");
    assert.equal(value(peer, "HSID"), "hsid-one");
  }
  assert.equal(seedValue(m, "SSID"), "ssid-one");
  assert.equal(value(a, "other", ".example.org"), "o1", "other sites flow both ways");

  logOut(c);
  await m.tick();
  await m.tick();
  assert.equal(value(c, "HSID"), undefined, "C stays signed out");
  assert.equal(value(a, "HSID"), "hsid-one", "the sign-out does not spread");
  assert.equal(value(b, "SID"), "sid-one");
  assert.equal(seedValue(m, "HSID"), "hsid-one", "the shared login is kept");
  assert.equal(value(a, "NID"), undefined, "C's logged-out cookies for that site are not mirrored");
  assert.deepEqual(Object.keys(m.state(c).blocked), ["google.com"]);

  put(a, google("SID", "sid-one-refreshed", { httpOnly: false }));
  put(a, site(".example.org", "other", "o2"));
  await m.tick();
  assert.equal(value(b, "SID"), "sid-one-refreshed", "a change in one browser overwrites the others");
  assert.equal(value(c, "SID"), undefined, "the blocked site still does not reach C");
  assert.equal(value(c, "other", ".example.org"), "o2", "other sites keep syncing to C");

  c = m.restart(c);
  await m.sync.runRoot(c.root, { publish: false });
  await m.tick();
  assert.equal(value(c, "HSID"), undefined, "C stays signed out across a relaunch");
  assert.equal(value(c, "other", ".example.org"), "o2");

  const d = m.start("d");
  await m.sync.runRoot(d.root, { publish: false });
  assert.equal(value(d, "HSID"), "hsid-one", "a new session starts signed in");

  logIn(c, "two");
  await m.tick();
  for (const peer of [a, b, d]) {
    assert.equal(value(peer, "SID"), "sid-two", "C's new login replaces the shared one");
    assert.equal(value(peer, "HSID"), "hsid-two");
  }
  assert.equal(seedValue(m, "SSID"), "ssid-two");
  assert.deepEqual(m.state(c).blocked, {});

  put(a, google("SID", "sid-two-refreshed", { httpOnly: false }));
  await m.tick();
  assert.equal(value(c, "SID"), "sid-two-refreshed", "C receives again once it is signed in");
});

test("sign-out state survives a daemon restart", async (t) => {
  const m = machine(t);
  const a = m.start("a");
  const c = m.start("c");
  await m.tick();
  logIn(a, "one");
  await m.tick();
  logOut(c);
  await m.tick();
  m.restartDaemon();
  await m.tick();
  assert.equal(value(c, "HSID"), undefined);
  assert.equal(value(a, "HSID"), "hsid-one");
});

test("losing a script cookie or a short-lived cookie is not a sign-out", async (t) => {
  const m = machine(t);
  const a = m.start("a");
  const b = m.start("b");
  await m.tick();
  logIn(a, "one");
  put(a, google("consent", "yes", { httpOnly: false }));
  put(a, google("csrf", "t1", { expires: NOW / 1000 + 600 }));
  await m.tick();
  drop(b, "consent");
  drop(b, "csrf");
  await m.tick();
  assert.deepEqual(m.state(b).blocked, {});
  assert.equal(seedValue(m, "consent"), undefined, "a plain deletion leaves the shared store");
  assert.equal(value(a, "consent"), "yes", "but is not deleted from other browsers");
  put(a, google("SID", "sid-next", { httpOnly: false }));
  await m.tick();
  assert.equal(value(b, "SID"), "sid-next", "B still receives the site");
});

test("re-creating one short session cookie after a sign-out is not a new login", async (t) => {
  const m = machine(t);
  const sess = (v) => site("shop.example", "PHPSESSID", v, { session: true, expires: -1 });
  const a = m.start("a", { cookies: [sess("logged-in")] });
  const c = m.start("c");
  await m.tick();
  await m.tick();
  assert.equal(value(c, "PHPSESSID", "shop.example"), "logged-in");
  drop(c, "PHPSESSID", "shop.example");
  await m.tick();
  put(c, sess("anonymous"));
  await m.tick();
  assert.equal(value(a, "PHPSESSID", "shop.example"), "logged-in");
  assert.deepEqual(Object.keys(m.state(c).blocked), ["shop.example"]);
});

test("one long-lived login cookie coming back is a new login", async (t) => {
  const m = machine(t);
  const sess = (v) => site("app.example", "sessionid", v);
  const a = m.start("a", { cookies: [sess("old")] });
  const c = m.start("c");
  await m.tick();
  await m.tick();
  drop(c, "sessionid", "app.example");
  await m.tick();
  put(c, sess("new"));
  await m.tick();
  assert.equal(value(a, "sessionid", "app.example"), "new");
});

// Google revokes the whole session when a request carries SID without its
// __Secure-*PSIDTS, so a new browser must start with the latest rotating values.
test("rotating Google cookies fill gaps only, and the store keeps the latest for new browsers", async (t) => {
  const m = machine(t);
  const a = m.start("a");
  const b = m.start("b");
  await m.tick();
  logIn(a, "one");
  put(a, google("__Secure-1PSIDTS", "a-rot"));
  put(b, google("SIDCC", "b-own"));
  await m.tick();
  assert.equal(value(b, "__Secure-1PSIDTS"), "a-rot");
  put(a, google("SIDCC", "a-own"));
  put(a, google("__Secure-1PSIDTS", "a-rot-2"));
  await m.tick();
  assert.equal(value(b, "SIDCC"), "b-own", "a running browser keeps its own rotating cookie");
  assert.equal(value(b, "__Secure-1PSIDTS"), "a-rot");
  assert.equal(seedValue(m, "__Secure-1PSIDTS"), "a-rot-2", "the store keeps the latest");
  const d = m.start("d");
  await m.sync.runRoot(d.root, { publish: false });
  assert.equal(value(d, "__Secure-1PSIDTS"), "a-rot-2", "a new browser starts with SID and its PSIDTS");
  assert.equal(value(d, "SID"), "sid-one");
});

test("--capture of an unknown browser publishes its own values", async (t) => {
  const m = machine(t);
  const a = m.start("a");
  await m.tick();
  logIn(a, "old");
  await m.tick();
  m.stop(a);
  const c = m.start("c", { cookies: [google("HSID", "hsid-mine")] });
  await m.sync.runRoot(c.root, { publish: true });
  assert.equal(seedValue(m, "HSID"), "hsid-mine");
  assert.equal(value(c, "SID"), "sid-old", "it still receives what it lacks");
});

test("a refused cookie is not pushed again every pass", async (t) => {
  const m = machine(t);
  const a = m.start("a", { cookies: [google("BAD", "x")] });
  const b = m.start("b");
  b.refused.add("BAD");
  let calls = 0;
  const send = b.send.bind(b);
  b.send = (method, ...rest) => {
    if (method === "Storage.setCookies") calls++;
    return send(method, ...rest);
  };
  await m.tick();
  const first = calls;
  await m.tick();
  assert.ok(first > 0);
  assert.equal(calls, first);
  assert.equal(value(a, "BAD"), "x");
});

test("login cookie changes reload receiver pages; busy pages wait", async (t) => {
  const m = machine(t);
  const a = m.start("a", { pages: [["https://mail.google.com", []]] });
  const b = m.start("b", { pages: [["https://mail.google.com", []]] });
  await m.tick();
  logIn(a, "one");
  fs.writeFileSync(join(b.root, ".busy"), "");
  await m.tick();
  assert.equal(value(b, "HSID"), "hsid-one");
  assert.equal(b.reloads, 0, "busy session is not reloaded");
  fs.rmSync(join(b.root, ".busy"));
  await m.tick();
  assert.equal(b.reloads, 1, "withheld reload is delivered once");
  await m.tick();
  assert.equal(b.reloads, 1);
  assert.equal(a.reloads, 0, "the writer is not reloaded");
});

test("localStorage fills missing keys but skips a blocked site", async (t) => {
  const m = machine(t);
  const origin = "https://mail.google.com";
  const a = m.start("a", { pages: [[origin, [["token", "t1"]]]] });
  const b = m.start("b", { pages: [[origin, []]] });
  const c = m.start("c", { pages: [[origin, []]] });
  await m.tick();
  logIn(a, "one");
  await m.tick();
  logOut(c);
  c.pages[0].items.clear();
  await m.tick();
  a.pages[0].items.set("next", "n1");
  await m.tick();
  assert.equal(b.pages[0].items.get("next"), "n1");
  assert.equal(c.pages[0].items.has("next"), false);
});

test("reload breaker trips per browser and host and reopens after quiet", () => {
  let now = 0;
  const breaker = new ReloadBreaker(() => now);
  for (let i = 0; i < RELOAD_MAX_PER_WINDOW; i++) assert.equal(breaker.request("a", "h"), "allow");
  assert.equal(breaker.request("a", "h"), "trip");
  assert.equal(breaker.request("a", "h"), "open");
  assert.equal(breaker.request("b", "h"), "allow");
  now += RELOAD_QUIET_MS;
  assert.deepEqual(breaker.takeDue(new Map([["a", new Set(["h"])]])), [{ id: "a", host: "h" }]);
});

test("planSync takes the later expiry when two browsers write the same key", () => {
  const view = {};
  const state = (id) => ({ version: 1, browserId: id, view, attempted: {}, blocked: {} });
  const older = google("HSID", "older", { expires: YEAR - 10 });
  const newer = google("HSID", "newer");
  const plan = planSync({
    canonical: new Map(),
    browsers: [
      { id: "a", browserId: "a", jar: jar([older]), state: state("a") },
      { id: "b", browserId: "b", jar: jar([newer]), state: state("b") },
    ],
    now: NOW,
  });
  assert.equal(plan.canonical.get(cookieKey(newer)).value, "newer");
  assert.deepEqual(plan.pushes.get("a").map((c) => c.value), ["newer"]);
});

test("CLI rejects invalid usage and --once exits with no browsers", (t) => {
  const dir = temporary(t);
  const cli = fileURLToPath(new URL("../wk-browser-sync.mjs", import.meta.url));
  const env = { ...process.env, WK_BROWSER_AUTH_DIR: join(dir, "auth"),
    WK_BROWSER_SESSIONS_DIR: join(dir, "none"), WK_BROWSER_SHARED_ROOT: join(dir, "none2"),
    WK_BROWSER_PROC_ROOT: join(dir, "no-proc") };
  const run = (args) => spawnSync(process.execPath, [cli, ...args], { env, timeout: 5000, encoding: "utf8" });
  assert.equal(run(["bogus"]).status, 2);
  assert.equal(run(["--restore-root"]).status, 2);
  assert.equal(run(["--capture", "--once"]).status, 2);
  const result = run(["--once"]);
  assert.equal(result.status, 0, result.stderr);
  assert.notEqual(run(["--restore-root", join(dir, "missing.browser")]).status, 0);
});
