import assert from "node:assert/strict";
import * as fs from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

// Run inside a disposable container with Chrome:
//   node --experimental-websocket /work/tests/test_browser_sync_live.mjs
const scripts = dirname(dirname(fileURLToPath(import.meta.url)));
const scratch = fs.mkdtempSync(join(tmpdir(), "wk-sync-live-"));
const sessions = join(scratch, "sessions");
fs.mkdirSync(sessions);
process.env.WK_BROWSER_SESSIONS_DIR = sessions;
process.env.WK_BROWSER_SHARED_ROOT = join(scratch, "unused-single.browser");
process.env.WK_BROWSER_AUTH_DIR = join(scratch, "auth");
process.env.WK_CHROME_SHARED = join(scratch, "shared");

const processes = new Set();
const connections = new Set();
const requests = [];
let sync;
let server;
let stopping;

async function eventually(description, probe, timeout = 15000) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    try {
      const value = await probe();
      if (value) return value;
    } catch (error) { last = error; }
    await delay(75);
  }
  throw new Error(`Timed out: ${description}`, { cause: last });
}

function launch(command, args, options = {}) {
  const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"], detached: true, ...options });
  processes.add(child);
  child.output = "";
  child.stdout.on("data", (chunk) => { child.output = (child.output + chunk).slice(-16000); });
  child.stderr.on("data", (chunk) => { child.output = (child.output + chunk).slice(-16000); });
  child.on("error", (error) => { child.launchError = error; });
  return child;
}

function killGroup(child, signal) {
  if (!child.pid) return;
  try { process.kill(-child.pid, signal); } catch (error) {
    if (error.code !== "ESRCH") throw error;
  }
}

async function stop(child) {
  if (!child) return;
  killGroup(child, "SIGTERM");
  const deadline = Date.now() + 3000;
  while (child.exitCode == null && child.signalCode == null && !child.launchError && Date.now() < deadline) {
    await delay(50);
  }
  killGroup(child, "SIGKILL");
  processes.delete(child);
}

async function cleanup() {
  if (stopping) return stopping;
  stopping = (async () => {
    sync?.close();
    for (const browser of connections) browser.close();
    await Promise.all([...processes].map(stop));
    if (server) {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    }
    fs.rmSync(scratch, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  })();
  return stopping;
}

const onSignal = (signal) => {
  void cleanup().finally(() => process.exit(signal === "SIGINT" ? 130 : 143));
};
process.once("SIGINT", onSignal);
process.once("SIGTERM", onSignal);
const watchdog = setTimeout(() => {
  console.error("FAIL: live sync test exceeded 180 seconds");
  void cleanup().finally(() => process.exit(1));
}, 180000);

// A site that keeps its login in two long-lived httpOnly cookies, like most
// real sites, and deletes them on logout.
function serveSite(req, res) {
  const url = new URL(req.url, "http://x");
  const cookies = Object.fromEntries((req.headers.cookie ?? "").split("; ").filter(Boolean)
    .map((pair) => [pair.slice(0, pair.indexOf("=")), pair.slice(pair.indexOf("=") + 1)]));
  requests.push({ path: url.pathname, cookies });
  const headers = { "Content-Type": "text/html", "Cache-Control": "no-store" };
  if (url.pathname === "/login") {
    // Real sign-ins redirect away, so reloading the landing page never signs in again.
    const user = url.searchParams.get("u");
    res.writeHead(302, { Location: "/", "Set-Cookie": [
      `site_session=${user}-${Date.now()}; Path=/; HttpOnly; Max-Age=31536000; SameSite=Lax`,
      `site_user=${user}; Path=/; HttpOnly; Max-Age=31536000; SameSite=Lax`,
      `site_pref=dark; Path=/; Max-Age=31536000; SameSite=Lax`,
    ] });
    res.end();
    return;
  } else if (url.pathname === "/logout") {
    headers["Set-Cookie"] = [
      "site_session=; Path=/; HttpOnly; Max-Age=0",
      "site_user=; Path=/; HttpOnly; Max-Age=0",
    ];
  }
  res.writeHead(200, headers);
  const who = url.pathname === "/logout" ? "" : cookies.site_user ?? "";
  res.end(`<!doctype html><title>${who ? `user:${who}` : "logged out"}</title>
    <script>window.boot = crypto.randomUUID();</script>`);
}

async function main() {
  assert.equal(typeof WebSocket, "function", "Use Node >=22 or Node 20 with --experimental-websocket");
  const { connectBrowser, readCookies } = await import("../cdp-cookies.mjs");
  const { BrowserSync, readSeed } = await import("../wk-browser-sync.mjs");
  const cli = join(scripts, "wk-browser-sync.mjs");
  const origin = "http://127.0.0.1:18091";
  const seedPath = join(process.env.WK_BROWSER_AUTH_DIR, "chrome-cookie-seed.json");

  server = createServer(serveSite);
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(18091, "127.0.0.1", resolve);
  });

  async function runCli(args) {
    const child = launch(process.execPath, [...process.execArgv, cli, ...args], { env: { ...process.env } });
    await eventually(`${args.join(" ")} exits`, () => child.launchError || child.exitCode != null || child.signalCode != null);
    assert.ifError(child.launchError);
    assert.equal(child.exitCode, 0, `${args.join(" ")} failed: ${child.output}`);
    await stop(child);
  }

  // Mirrors the wrapper: link the profile, launch with .launching set,
  // restore before the first page, then join the machine.
  async function startChrome(name) {
    const root = join(sessions, `${name}.browser`);
    const profile = join(root, "profile");
    fs.mkdirSync(profile, { recursive: true });
    const linked = launch("bash", [join(scripts, "link-chrome-session.sh"), profile]);
    await eventually(`${name} profile linking exits`, () => linked.launchError || linked.exitCode != null);
    assert.equal(linked.exitCode, 0, linked.output);
    await stop(linked);
    fs.rmSync(join(profile, "DevToolsActivePort"), { force: true });
    fs.writeFileSync(join(root, ".launching"), "");
    const child = launch("/usr/bin/google-chrome", [
      "--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu",
      "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
      "--disable-extensions", "--password-store=basic", "--remote-debugging-port=0",
      `--user-data-dir=${profile}`, "about:blank",
    ]);
    const port = await eventually(`${name} DevToolsActivePort`, async () => {
      if (child.launchError) throw child.launchError;
      if (child.exitCode != null) throw new Error(`Chrome exited: ${child.output}`);
      const value = Number(fs.readFileSync(join(profile, "DevToolsActivePort"), "utf8").split("\n")[0]);
      if (!(value > 0)) return false;
      const response = await fetch(`http://127.0.0.1:${value}/json/version`, { signal: AbortSignal.timeout(1000) });
      return response.ok && value;
    });
    await runCli(["--restore-root", root]);
    fs.rmSync(join(root, ".launching"));
    const browser = await connectBrowser(port);
    connections.add(browser);
    const { targetInfos } = await browser.send("Target.getTargets");
    const target = targetInfos.find((item) => item.type === "page");
    const { sessionId } = await browser.send("Target.attachToTarget", { targetId: target.targetId, flatten: true });
    await browser.send("Page.enable", {}, sessionId);
    return { name, root, child, port, browser, sessionId };
  }

  async function quitChrome(seat) {
    seat.browser.close();
    connections.delete(seat.browser);
    // Chrome flushes cookies to disk on a clean exit.
    killGroup(seat.child, "SIGTERM");
    await eventually(`${seat.name} exits`, () => seat.child.exitCode != null || seat.child.signalCode != null);
    await stop(seat.child);
  }

  async function evaluate(seat, expression) {
    const reply = await seat.browser.send("Runtime.evaluate", { expression, returnByValue: true }, seat.sessionId);
    assert.ok(!reply.exceptionDetails, `${seat.name} evaluation: ${JSON.stringify(reply.exceptionDetails)}`);
    return reply.result?.value;
  }

  async function navigate(seat, path) {
    const before = requests.length;
    const landing = path.startsWith("/login") ? "/" : path;
    await seat.browser.send("Page.navigate", { url: `${origin}${path}` }, seat.sessionId);
    await eventually(`${seat.name} loaded ${path}`, async () => requests.length > before &&
      await evaluate(seat, `location.href === ${JSON.stringify(origin + landing)} && document.readyState === 'complete' && !!window.boot`));
    return requests.findLast((r) => r.path === landing);
  }

  const userOf = async (seat) => (await navigate(seat, "/")).cookies.site_user;
  const cookie = async (seat, name) =>
    [...(await readCookies(seat.browser)).values()].find((c) => c.name === name)?.value;
  const seedUser = () => readSeed(fs, seedPath).cookies.find((c) => c.name === "site_user")?.value;
  const tick = () => sync.tick();

  sync = new BrowserSync({ authDir: process.env.WK_BROWSER_AUTH_DIR });
  const a = await startChrome("a");
  const b = await startChrome("b");
  let c = await startChrome("c");
  await tick();
  assert.equal(sync.browsers.size, 3, "all session browsers are discovered");

  await navigate(a, "/login?u=alice");
  await navigate(b, "/");
  await tick();
  assert.equal(await userOf(b), "alice");
  assert.equal(await userOf(c), "alice");
  assert.equal(seedUser(), "alice");
  console.log("PASS A signs in; B and C are signed in without logging in");

  await navigate(c, "/logout");
  await tick();
  await tick();
  assert.equal(await userOf(c), undefined, "C stays signed out");
  assert.equal(await userOf(a), "alice", "A keeps its login");
  assert.equal(await userOf(b), "alice", "B keeps its login");
  assert.equal(seedUser(), "alice", "the shared login store keeps the login");
  console.log("PASS C signs out alone; the sign-out does not spread");

  const sessionBefore = await cookie(a, "site_session");
  await navigate(a, "/login?u=alice");
  await tick();
  assert.notEqual(await cookie(b, "site_session"), sessionBefore, "B receives A's refreshed session");
  assert.equal(await cookie(b, "site_session"), await cookie(a, "site_session"));
  assert.equal(await cookie(c, "site_session"), undefined, "C receives nothing for the site");
  console.log("PASS a signed-out session no longer receives the site's login");

  await quitChrome(c);
  sync.close();
  c = await startChrome("c");
  await tick();
  assert.equal(await userOf(c), undefined, "C relaunches signed out");
  console.log("PASS C stays signed out after its browser is closed and reopened");

  const d = await startChrome("d");
  assert.equal(await userOf(d), "alice", "the very first request of a new session is signed in");
  console.log("PASS a new session starts signed in from the store");

  const bBoot = await evaluate(b, "window.boot");
  await navigate(c, "/login?u=carol");
  await tick();
  for (const seat of [a, d]) assert.equal(await userOf(seat), "carol", `${seat.name} follows C's new login`);
  await eventually("B's open page reloads with the new login", async () =>
    await evaluate(b, `window.boot !== ${JSON.stringify(bBoot)} && document.title === 'user:carol'`));
  assert.equal(seedUser(), "carol");
  assert.equal(await cookie(c, "site_pref"), "dark", "C's non-login cookies came with it");
  console.log("PASS C signs in again; its login replaces everyone's and open pages reload");

  await navigate(a, "/login?u=alice");
  await tick();
  assert.equal(await userOf(c), "alice", "C receives again once it is signed in");
  console.log("PASS C rejoins normal syncing");

  await quitChrome(b);
  await runCli(["--once"]);
  const fresh = await startChrome("b");
  assert.equal(await userOf(fresh), "alice");
  console.log("PASS one-shot CLI pass and relaunch restore agree");
}

try {
  await main();
  console.log("PASS all live browser sync checks");
} catch (error) {
  console.error(error);
  for (const child of processes) {
    if (child.launchError || child.exitCode != null) console.error(child.launchError ?? child.output);
  }
  process.exitCode = 1;
} finally {
  clearTimeout(watchdog);
  try { await cleanup(); } finally {
    process.removeListener("SIGINT", onSignal);
    process.removeListener("SIGTERM", onSignal);
  }
}
