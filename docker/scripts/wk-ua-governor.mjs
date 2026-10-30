import { rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

import {
  connectBrowser,
  discoverBrowserEndpoints,
  getBrowserVersion,
  isCdpTargetGone,
  isConnectionRefused,
} from "./cdp-cookies.mjs";
import { isUtcTimeZone } from "./wk-locale-match.mjs";
import { readBoxTimeZone, resolveEgressTimeZone } from "./wk-egress-timezone.mjs";
import { LINUX_PROFILE, buildUserAgentOverride } from "./wk-fingerprint-profiles.mjs";

export const GOVERNED_MARKER = ".ua-governed";
export const TZ_ATTACH_WAIT_MS = 3_000;
export const TZ_RETRY_MS = Object.freeze([5_000, 30_000, 120_000, 300_000]);
const POLL_MS = 500;

const reported = new Set();
function reportOnce(message) {
  if (reported.has(message)) return;
  reported.add(message);
  process.stderr.write(`wk-ua-governor ${message}\n`);
}
function errorClass(error) {
  return error?.code ?? error?.cause?.code ?? (error instanceof Error ? error.name : typeof error);
}

/*
Chrome auto-attach reaches only the direct children of the session it is
called on, so each page, frame and worker attaches its own out-of-process
iframes and workers
(https://chromedevtools.github.io/devtools-protocol/tot/Target/#method-setAutoAttach).
*/
const RELATED_FILTER = [
  { type: "iframe", exclude: false },
  { type: "worker", exclude: false },
  { type: "shared_worker", exclude: false },
  { exclude: true },
];
const BROWSER_FILTER = [
  { type: "service_worker", exclude: false },
  { type: "page", exclude: false },
  { type: "worker", exclude: false },
  { type: "shared_worker", exclude: false },
  { exclude: true },
];
const RELATED_FILTER_WITH_SW = [{ type: "service_worker", exclude: false }, ...RELATED_FILTER];
function attachRelated(browser, sessionId) {
  if (browser.relatedSessions.has(sessionId)) return Promise.resolve();
  browser.relatedSessions.add(sessionId);
  return browser
    .send(
      "Target.setAutoAttach",
      { autoAttach: true, waitForDebuggerOnStart: true, flatten: true, filter: RELATED_FILTER_WITH_SW },
      sessionId,
    )
    .catch((error) => {
      browser.relatedSessions.delete(sessionId);
      throw error;
    });
}

function applyTimeZone(browser, sessionId) {
  const timezoneId = browser.tz.zone;
  if (typeof timezoneId !== "string") return null;
  return browser.send("Emulation.setTimezoneOverride", { timezoneId }, sessionId);
}

export function reassertTimeZone(browser) {
  for (const sessionId of browser.attachedSessions) {
    applyTimeZone(browser, sessionId)?.catch((error) => {
      if (isCdpTargetGone(error) || !browser.attachedSessions.has(sessionId)) return;
      reportOnce(`time zone apply failed: ${errorClass(error)}`);
    });
  }
}

export async function applyToTarget(browser, sessionId, target) {
  const work = [
    browser.send("Emulation.setUserAgentOverride", buildUserAgentOverride(browser.chromeVersion), sessionId),
    browser.send(
      "Emulation.setHardwareConcurrencyOverride",
      { hardwareConcurrency: LINUX_PROFILE.hardwareConcurrency },
      sessionId,
    ),
    applyTimeZone(browser, sessionId),
  ];
  if (target?.type !== "service_worker") work.push(attachRelated(browser, sessionId));
  await Promise.all(work);
}

/*
Zone choice: the browser's egress geo when the lookup answers, else the box
clock zone (unless that is UTC, which says nothing about where the user is).
The page start is held for a short window so the first script already sees the
final zone; after it, the fallback is applied and the lookup keeps retrying.
*/
export function startTimeZone(browser, {
  resolveZone = ({ profile }) => resolveEgressTimeZone({ profile }),
  boxZone = () => readBoxTimeZone(),
  now = Date.now,
} = {}) {
  let release;
  const hold = new Promise((resolve) => { release = resolve; });
  browser.tz = {
    zone: null, resolved: false, attempts: 0, nextAt: now(), holdUntil: now() + TZ_ATTACH_WAIT_MS,
    inFlight: null, hold, release, resolveZone, boxZone,
  };
  return tickTimeZone(browser, { now });
}

function useBoxZone(browser) {
  const tz = browser.tz;
  tz.release();
  tz.hold = null;
  if (tz.zone != null) return;
  let zone = null;
  try { zone = tz.boxZone(); } catch (error) {
    reportOnce(`box time zone unreadable: ${errorClass(error)}`);
  }
  if (typeof zone === "string" && !isUtcTimeZone(zone)) {
    tz.zone = zone;
    reassertTimeZone(browser);
  }
}

export function tickTimeZone(browser, { now = Date.now } = {}) {
  const tz = browser.tz;
  if (tz == null) return null;
  if (tz.hold != null && now() >= tz.holdUntil) useBoxZone(browser);
  if (browser.isClosed || tz.resolved || tz.inFlight != null || now() < tz.nextAt) return tz.inFlight;
  tz.inFlight = (async () => {
    try {
      const { timeZone, provider, source } = await tz.resolveZone({ profile: browser.profile });
      if (browser.isClosed) return;
      tz.zone = timeZone;
      tz.resolved = true;
      reassertTimeZone(browser);
      reportOnce(`time zone ${timeZone} from ${provider}/${source}`);
    } catch (error) {
      reportOnce(`egress time zone unresolved: ${errorClass(error)}`);
      tz.nextAt = now() + TZ_RETRY_MS[Math.min(tz.attempts, TZ_RETRY_MS.length - 1)];
      tz.attempts += 1;
    } finally {
      tz.inFlight = null;
      if (tz.hold != null) useBoxZone(browser);
    }
  })();
  return tz.inFlight;
}

export async function governBrowser(browser, timeZoneOptions) {
  browser.attachedSessions = new Set();
  browser.attachedTypes = new Map();
  browser.relatedSessions = new Set();
  startTimeZone(browser, timeZoneOptions);
  browser.onEvent((message) => {
    const sessionId = message.params?.sessionId;
    if (typeof sessionId !== "string") return;
    if (message.method === "Target.detachedFromTarget") {
      browser.attachedSessions.delete(sessionId);
      browser.attachedTypes.delete(sessionId);
      browser.relatedSessions.delete(sessionId);
      return;
    }
    if (message.method !== "Target.attachedToTarget") return;
    browser.attachedSessions.add(sessionId);
    const type = message.params?.targetInfo?.type;
    if (typeof type === "string") browser.attachedTypes.set(sessionId, type);
    const quiet = (step) => (error) => {
      if (isCdpTargetGone(error) || !browser.attachedSessions.has(sessionId)) return;
      reportOnce(`${step} failed: ${errorClass(error)}`);
    };
    const treatAndResume = () => {
      void applyToTarget(browser, sessionId, { type }).catch(quiet("apply"));
      void browser.send("Runtime.runIfWaitingForDebugger", {}, sessionId).catch(quiet("resume"));
    };
    const hold = browser.tz?.hold;
    if (hold == null) treatAndResume();
    else void hold.then(treatAndResume);
  });
  await browser.send("Target.setAutoAttach", {
    autoAttach: true, waitForDebuggerOnStart: true, flatten: true, filter: BROWSER_FILTER,
  });
}

export function markGoverned(root, browserId, write = writeFileSync) {
  write(join(root, GOVERNED_MARKER), `${browserId}\n`);
}

export function clearGoverned(root, remove = rmSync) {
  remove(join(root, GOVERNED_MARKER), { force: true });
}

export async function tick(browsers, {
  discover = () => discoverBrowserEndpoints({ includeLaunching: true }),
  connect = connectBrowser,
  version = getBrowserVersion,
  govern = governBrowser,
  mark = markGoverned,
  clear = clearGoverned,
} = {}) {
  const endpoints = discover();
  const live = new Set(endpoints.map(({ root }) => root));
  for (const [root, browser] of browsers) {
    if (browser.isClosed || !live.has(root)) {
      browser.close();
      browsers.delete(root);
      try { clear(root); } catch (error) { reportOnce(`marker clear failed: ${errorClass(error)}`); }
    }
  }
  for (const { root, profile, port, browserId } of endpoints) {
    const existing = browsers.get(root);
    if (existing != null && existing.browserId === browserId) continue;
    existing?.close();
    browsers.delete(root);
    try {
      const chromeVersion = await version(port);
      const browser = await connect(port);
      browser.chromeVersion = chromeVersion;
      browser.profile = profile;
      browser.root = root;
      await govern(browser);
      browsers.set(root, browser);
      mark(root, browser.browserId);
    } catch (error) {
      if (!isConnectionRefused(error)) reportOnce(`browser on CDP port ${port} not governed: ${errorClass(error)}`);
    }
  }
  for (const browser of browsers.values()) void tickTimeZone(browser);
}

export async function main() {
  const controller = new AbortController();
  const stop = () => controller.abort();
  process.once("SIGTERM", stop);
  process.once("SIGINT", stop);
  const browsers = new Map();
  try {
    while (!controller.signal.aborted) {
      try { await tick(browsers); } catch (error) { reportOnce(`tick failed: ${errorClass(error)}`); }
      try { await delay(POLL_MS, undefined, { signal: controller.signal }); } catch { break; }
    }
  } finally {
    for (const browser of browsers.values()) browser.close();
  }
}

if (process.argv[1] != null && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => { reportOnce(`driver failed: ${errorClass(error)}`); process.exitCode = 1; });
}
