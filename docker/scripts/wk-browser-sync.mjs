import * as nodeFs from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import {
  connectBrowser,
  cookieKey,
  discoverBrowserEndpoints,
  isConnectionRefused,
  pushCookies,
  readBrowserRootEndpoint,
  readCookies,
  toCookieParam,
} from "./cdp-cookies.mjs";
import {
  cookieDigest,
  emptyRootState,
  planSync,
  ROOT_STATE_VERSION,
  siteOf,
  viewOf,
} from "./wk-cookie-plan.mjs";
import { PageSync } from "./wk-page-sync.mjs";

export const DEFAULT_AUTH_DIR = "/var/lib/weknora/browser/auth";
export const SEED_FILE = "chrome-cookie-seed.json";
export const ROOT_STATE_FILE = ".wk-sync-state.json";
export const SEED_VERSION = 1;
export const BUSY_TTL_MS = 30_000;
const LOCK_TIMEOUT_MS = 20_000;
const LOCK_RETRY_MS = 50;
const USAGE = "usage: wk-browser-sync [run|--once|--restore-root ROOT|--capture ROOT]\n";

function log(message) {
  process.stderr.write(`wk-browser-sync ${message}\n`);
}

function errorCode(error) {
  return error?.code ?? error?.cause?.code ?? "unknown";
}

function writePrivateFile(fs, path, text) {
  const tmp = `${path}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, text, { mode: 0o600 });
  fs.renameSync(tmp, path);
}

function pidAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code === "EPERM";
  }
}

export async function withLock(fs, lockDir, fn, { timeoutMs = LOCK_TIMEOUT_MS } = {}) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      fs.mkdirSync(lockDir);
      fs.writeFileSync(join(lockDir, "owner"), String(process.pid));
      break;
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
      let owner = NaN;
      try {
        owner = Number(fs.readFileSync(join(lockDir, "owner"), "utf8"));
      } catch {}
      const age = (() => {
        try {
          return Date.now() - fs.statSync(lockDir).mtimeMs;
        } catch {
          return 0;
        }
      })();
      if (Number.isInteger(owner) && owner > 0 ? !pidAlive(owner) : age > 5_000) {
        fs.rmSync(lockDir, { recursive: true, force: true });
        continue;
      }
      if (Date.now() >= deadline) throw Object.assign(new Error("sync lock busy"), { code: "ETIMEDOUT" });
      await delay(LOCK_RETRY_MS);
    }
  }
  try {
    return await fn();
  } finally {
    fs.rmSync(lockDir, { recursive: true, force: true });
  }
}

export function serializeSeed(cookies, now = Date.now()) {
  return JSON.stringify({ version: SEED_VERSION, savedAt: now, cookies });
}

export function readSeed(fs, path) {
  let text;
  try {
    text = fs.readFileSync(path, "utf8");
  } catch (error) {
    if (error.code === "ENOENT") return { kind: "absent", cookies: [] };
    throw error;
  }
  try {
    const parsed = JSON.parse(text);
    if (parsed?.version === SEED_VERSION && Array.isArray(parsed.cookies)) {
      return { kind: "ok", cookies: parsed.cookies.filter((c) => typeof c?.name === "string") };
    }
  } catch {}
  return { kind: "corrupt", cookies: [] };
}

export function readRootState(fs, root) {
  try {
    const parsed = JSON.parse(fs.readFileSync(join(root, ROOT_STATE_FILE), "utf8"));
    if (parsed?.version === ROOT_STATE_VERSION) return { ...emptyRootState(), ...parsed };
  } catch {}
  return emptyRootState();
}

function canonicalDigest(canonical) {
  return [...canonical].map(([key, cookie]) => `${key}\u0001${cookieDigest(cookie)}\u0001${cookie.expires}`)
    .sort().join("\u0002");
}

function isBusy(fs, root, now) {
  try {
    return now - fs.statSync(join(root, ".busy")).mtimeMs < BUSY_TTL_MS;
  } catch {
    return false;
  }
}

export class BrowserSync {
  constructor({
    authDir = DEFAULT_AUTH_DIR,
    discover = discoverBrowserEndpoints,
    connect = connectBrowser,
    fs = nodeFs,
    now = Date.now,
  } = {}) {
    this.authDir = authDir;
    this.seedPath = join(authDir, SEED_FILE);
    this.lockDir = `${this.seedPath}.lock`;
    this.discover = discover;
    this.connect = connect;
    this.fs = fs;
    this.now = now;
    this.browsers = new Map();
    this.pages = new PageSync({ now, log });
  }

  async browserFor(endpoint) {
    const held = this.browsers.get(endpoint.root);
    if (held != null && !held.isClosed && held.port === endpoint.port && held.browserId === endpoint.browserId) {
      return held;
    }
    held?.close();
    this.browsers.delete(endpoint.root);
    try {
      const browser = await this.connect(endpoint.port);
      this.browsers.set(endpoint.root, browser);
      return browser;
    } catch (error) {
      if (!isConnectionRefused(error)) log(`connecting to ${endpoint.root} failed (${errorCode(error)})`);
      return null;
    }
  }

  // One locked pass over the given browsers. joinMode applies to browsers the
  // pass has not seen before; see planSync.
  async pass(endpoints, { joinMode = "receive" } = {}) {
    const members = [];
    for (const endpoint of endpoints) {
      const browser = await this.browserFor(endpoint);
      if (browser != null) members.push({ endpoint, browser });
    }
    if (members.length === 0) return { members: [], pushedSites: new Map(), events: [] };
    this.fs.mkdirSync(this.authDir, { recursive: true, mode: 0o700 });
    return withLock(this.fs, this.lockDir, () => this.lockedPass(members, joinMode));
  }

  async lockedPass(members, joinMode) {
    const { fs } = this;
    const seed = readSeed(fs, this.seedPath);
    if (seed.kind === "corrupt") {
      fs.renameSync(this.seedPath, `${this.seedPath}.corrupt-${this.now()}`);
      log("login store was corrupt; set it aside and started a new one");
    }
    const canonical = new Map(seed.cookies.map((cookie) => [cookieKey(cookie), cookie]));
    const before = canonicalDigest(canonical);

    const inputs = [];
    for (const { endpoint, browser } of members) {
      let jar;
      try {
        jar = await readCookies(browser);
      } catch (error) {
        log(`reading cookies of ${endpoint.root} failed (${errorCode(error)})`);
        browser.close();
        continue;
      }
      inputs.push({
        id: endpoint.root,
        browserId: browser.browserId,
        jar,
        state: readRootState(fs, endpoint.root),
        joinMode,
        browser,
      });
    }
    const plan = planSync({ canonical, browsers: inputs, now: this.now() });

    const pushedSites = new Map();
    const done = [];
    for (const input of inputs) {
      const state = plan.states.get(input.id);
      const push = plan.pushes.get(input.id) ?? [];
      let { jar } = input;
      try {
        if (push.length > 0) {
          await pushCookies(input.browser, push.map(toCookieParam));
          jar = await readCookies(input.browser);
          for (const cookie of push) {
            const key = cookieKey(cookie);
            const landed = jar.get(key);
            if (landed == null || cookieDigest(landed) !== cookieDigest(cookie)) {
              state.attempted[key] = cookieDigest(cookie);
            } else {
              delete state.attempted[key];
            }
          }
          pushedSites.set(input.id, new Set(push.filter((c) => c.httpOnly).map((c) => siteOf(c.domain))));
        }
      } catch (error) {
        log(`writing cookies into ${input.id} failed (${errorCode(error)})`);
        input.browser.close();
        continue;
      }
      state.view = viewOf(jar);
      writePrivateFile(fs, join(input.id, ROOT_STATE_FILE), `${JSON.stringify(state)}\n`);
      done.push({ ...input, state });
    }

    if (canonicalDigest(plan.canonical) !== before || seed.kind !== "ok") {
      writePrivateFile(fs, this.seedPath, serializeSeed([...plan.canonical.values()], this.now()));
    }
    for (const event of plan.events) {
      log(event.type === "signed-out"
        ? `${event.id} signed out of ${event.site}; keeping the shared login and no longer syncing it there`
        : `${event.id} signed in to ${event.site} again; its login now replaces the shared one`);
    }
    const pushed = [...plan.pushes.values()].reduce((n, list) => n + list.length, 0);
    if (pushed > 0) log(`wrote ${pushed} cookie(s) across ${inputs.length} browser(s)`);
    return { members: done, pushedSites, events: plan.events };
  }

  async tick() {
    const endpoints = this.discover();
    const live = new Set(endpoints.map(({ root }) => root));
    for (const [root, browser] of this.browsers) {
      if (!live.has(root)) {
        browser.close();
        this.browsers.delete(root);
      }
    }
    const { members, pushedSites } = await this.pass(endpoints);
    const now = Date.now();
    await this.pages.run(members.map(({ id, browserId, browser, state }) => ({
      id,
      browserId,
      browser,
      blocked: new Set(Object.keys(state.blocked)),
      busy: isBusy(this.fs, id, now),
    })), pushedSites);
  }

  // Launch restore deliberately bypasses discovery's .launching exclusion.
  async runRoot(root, { publish }) {
    const endpoint = readBrowserRootEndpoint(root);
    const { members } = await this.pass([endpoint], { joinMode: publish ? "publish" : "receive" });
    if (members.length === 0) throw Object.assign(new Error("browser unreachable"), { code: "ECONNREFUSED" });
  }

  close() {
    for (const browser of this.browsers.values()) browser.close();
    this.browsers.clear();
  }
}

function positiveInterval(text, fallback) {
  if (text == null) return fallback;
  const value = Number(text);
  if (!Number.isSafeInteger(value) || value < 1) throw new Error("invalid browser sync interval");
  return value;
}

export async function main(argv = process.argv.slice(2), env = process.env) {
  const [mode = "run", root] = argv;
  const rootMode = mode === "--restore-root" || mode === "--capture";
  if (rootMode ? argv.length !== 2 || !root || root.startsWith("-") :
    !["run", "--once"].includes(mode) || argv.length > 1) {
    process.stderr.write(USAGE);
    process.exitCode = 2;
    return;
  }
  const sync = new BrowserSync({ authDir: env.WK_BROWSER_AUTH_DIR ?? DEFAULT_AUTH_DIR });
  if (rootMode || mode === "--once") {
    try {
      if (rootMode) await sync.runRoot(root, { publish: mode === "--capture" });
      else await sync.tick();
    } finally {
      sync.close();
    }
    return;
  }
  const intervalMs = positiveInterval(env.WK_BROWSER_SYNC_INTERVAL_MS, 1500);
  const controller = new AbortController();
  const stop = () => controller.abort();
  process.once("SIGTERM", stop);
  process.once("SIGINT", stop);
  log("started");
  try {
    while (!controller.signal.aborted) {
      try {
        await sync.tick();
      } catch (error) {
        log(`pass failed (${errorCode(error)})`);
      }
      try {
        await delay(intervalMs, undefined, { signal: controller.signal });
      } catch {}
    }
  } finally {
    sync.close();
    process.removeListener("SIGTERM", stop);
    process.removeListener("SIGINT", stop);
  }
}

if (process.argv[1] != null && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    log(`failed (${errorCode(error)})`);
    process.exitCode = 1;
  });
}
