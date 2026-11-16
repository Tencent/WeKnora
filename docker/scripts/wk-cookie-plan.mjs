import { createHash } from "node:crypto";
import { cookieFingerprint, cookieRecency, isRotatingAuthCookie } from "./cdp-cookies.mjs";

export const DAY_MS = 24 * 60 * 60 * 1000;
export const ROOT_STATE_VERSION = 1;

// Second-level labels that sit under a two-letter country TLD as a public
// suffix (bbc.co.uk, example.com.cn). Not the full Public Suffix List.
const PUBLIC_SECOND_LEVEL = new Set([
  "ac", "co", "com", "edu", "go", "gob", "gov", "ltd", "mil", "ne", "net", "nom", "or", "org", "plc", "sch",
]);
// Hosting suffixes where every subdomain is a different owner.
const SHARED_HOSTING_SUFFIXES = [
  "github.io", "gitlab.io", "herokuapp.com", "vercel.app", "netlify.app", "pages.dev", "workers.dev",
  "appspot.com", "blogspot.com", "web.app", "firebaseapp.com", "azurewebsites.net", "cloudfront.net",
];

export function siteOf(domain) {
  const host = String(domain ?? "").toLowerCase().replace(/^\.+/, "").replace(/\.+$/, "");
  if (host === "" || /^[\d.]+$/.test(host) || host.includes(":")) return host;
  const labels = host.split(".");
  for (const suffix of SHARED_HOSTING_SUFFIXES) {
    if (host === suffix) return host;
    if (host.endsWith(`.${suffix}`)) return labels.slice(-(suffix.split(".").length + 1)).join(".");
  }
  if (labels.length <= 2) return host;
  const [sld, tld] = labels.slice(-2);
  const take = tld.length === 2 && PUBLIC_SECOND_LEVEL.has(sld) ? 3 : 2;
  return labels.slice(-take).join(".");
}

export function siteOfKey(key) {
  return siteOf(key.split("\u0000")[0]);
}

export function nameOfKey(key) {
  return key.split("\u0000")[2] ?? "";
}

// The digest leaves out the expiry: sliding-expiry sites refresh it on every
// request, and that is not a change worth mirroring.
export function cookieDigest(cookie) {
  return createHash("sha256").update(cookieFingerprint(cookie)).digest("hex").slice(0, 32);
}

function isSessionCookie(cookie) {
  return cookie.session === true || !(typeof cookie.expires === "number" && cookie.expires > 0);
}

export function viewEntry(cookie) {
  return {
    d: cookieDigest(cookie),
    h: cookie.httpOnly === true ? 1 : 0,
    s: isSessionCookie(cookie) ? 1 : 0,
    e: typeof cookie.expires === "number" ? cookie.expires : -1,
  };
}

export function viewOf(jar) {
  const view = {};
  for (const [key, cookie] of jar) view[key] = viewEntry(cookie);
  return view;
}

// Losing one of these is read as a sign-out. Sites keep login sessions in
// httpOnly cookies; script-set analytics and consent cookies come and go.
export function isCredential(name, entry, nowMs) {
  if (isRotatingAuthCookie(name) || entry.h !== 1) return false;
  if (entry.s === 1) return true;
  return entry.e * 1000 - nowMs >= DAY_MS;
}

function isLongLived(cookie, nowMs) {
  return !isSessionCookie(cookie) && cookie.expires * 1000 - nowMs >= DAY_MS;
}

function isExpired(cookie, nowMs) {
  return !isSessionCookie(cookie) && cookie.expires * 1000 <= nowMs;
}

export function emptyRootState() {
  return { version: ROOT_STATE_VERSION, browserId: null, view: null, attempted: {}, blocked: {} };
}

function cloneBlocked(blocked) {
  const copy = {};
  for (const [site, entry] of Object.entries(blocked ?? {})) {
    copy[site] = { keys: [...entry.keys], reacquired: [...(entry.reacquired ?? [])], at: entry.at ?? 0 };
  }
  return copy;
}

/**
 * One sync pass over the browsers it is given.
 *
 * browsers: [{ id, browserId, jar: Map<key, cookie>, state, joinMode? }]
 *   joinMode "receive" (default): a browser seen for the first time takes the
 *   canonical values; "publish": its own values win, as for `wk-auth save`.
 *
 * Returns the next canonical map, the cookies to push into each browser, the
 * next per-browser state (the caller sets `view` from the jar after pushing),
 * and sign-out / sign-in events.
 */
export function planSync({ canonical, browsers, now }) {
  const next = new Map(canonical);
  const states = new Map();
  const events = [];
  const writes = new Map();
  const takeovers = [];
  const joined = [];

  const recordWrite = (id, key, cookie) => {
    const prior = writes.get(key);
    if (prior == null || cookieRecency(cookie) > cookieRecency(prior.cookie)) writes.set(key, { id, cookie });
  };

  for (const browser of browsers) {
    const prev = browser.state ?? emptyRootState();
    const isNew = prev.browserId !== browser.browserId || prev.view == null;
    const state = {
      version: ROOT_STATE_VERSION,
      browserId: browser.browserId,
      view: prev.view,
      attempted: isNew ? {} : { ...prev.attempted },
      blocked: cloneBlocked(prev.blocked),
    };
    states.set(browser.id, state);
    const { jar } = browser;

    if (isNew) {
      for (const entry of Object.values(state.blocked)) {
        entry.reacquired = entry.reacquired.filter((key) => jar.has(key));
      }
      joined.push(browser);
      continue;
    }

    const signedOut = new Map();
    const dropped = [];
    for (const [key, before] of Object.entries(prev.view)) {
      if (jar.has(key)) continue;
      if (before.s !== 1 && before.e * 1000 <= now) continue;
      if (isCredential(nameOfKey(key), before, now)) {
        const site = siteOfKey(key);
        if (!signedOut.has(site)) signedOut.set(site, []);
        signedOut.get(site).push(key);
      } else {
        dropped.push([key, before]);
      }
    }
    for (const [site, keys] of signedOut) {
      const entry = state.blocked[site] ?? { keys: [], reacquired: [], at: now };
      entry.keys = [...new Set([...entry.keys, ...keys])];
      entry.reacquired = entry.reacquired.filter((key) => !keys.includes(key));
      entry.at = now;
      state.blocked[site] = entry;
      events.push({ type: "signed-out", id: browser.id, site });
    }
    for (const [key, before] of dropped) {
      if (state.blocked[siteOfKey(key)] != null) continue;
      const held = next.get(key);
      if (held != null && cookieDigest(held) === before.d) next.delete(key);
    }

    for (const [key, cookie] of jar) {
      if (prev.view[key]?.d === cookieDigest(cookie)) continue;
      const entry = state.blocked[siteOf(cookie.domain)];
      if (entry != null) {
        if (entry.keys.includes(key) && !entry.reacquired.includes(key)) entry.reacquired.push(key);
        continue;
      }
      recordWrite(browser.id, key, cookie);
    }

    for (const [site, entry] of Object.entries(state.blocked)) {
      entry.reacquired = entry.reacquired.filter((key) => jar.has(key));
      if (!entry.keys.every((key) => entry.reacquired.includes(key))) continue;
      const strong = entry.keys.length >= 2 || entry.keys.some((key) => isLongLived(jar.get(key), now));
      if (!strong) continue;
      delete state.blocked[site];
      takeovers.push({ browser, site });
      events.push({ type: "signed-in", id: browser.id, site });
    }
  }

  for (const [key, { cookie }] of writes) next.set(key, cookie);

  for (const browser of joined) {
    const { blocked } = states.get(browser.id);
    const publish = browser.joinMode === "publish";
    for (const [key, cookie] of browser.jar) {
      if (blocked[siteOf(cookie.domain)] != null) continue;
      if (publish || !next.has(key)) next.set(key, cookie);
    }
  }

  const forced = new Map();
  for (const { browser, site } of takeovers) {
    const own = [...browser.jar].filter(([, cookie]) => siteOf(cookie.domain) === site);
    for (const key of [...next.keys()]) {
      if (siteOfKey(key) === site && !browser.jar.has(key)) next.delete(key);
    }
    for (const [key, cookie] of own) next.set(key, cookie);
    forced.set(browser.id, [...(forced.get(browser.id) ?? []), { site, cookies: own }]);
  }

  for (const [key, cookie] of next) {
    if (isExpired(cookie, now)) next.delete(key);
  }

  const pushes = new Map();
  for (const browser of browsers) {
    const state = states.get(browser.id);
    const differs = (key, cookie) => {
      const held = browser.jar.get(key);
      return held == null || cookieDigest(held) !== cookieDigest(cookie);
    };
    const plan = new Map();
    for (const [key, cookie] of next) {
      if (state.blocked[siteOf(cookie.domain)] != null || !differs(key, cookie)) continue;
      if (isRotatingAuthCookie(cookie.name) && browser.jar.has(key)) continue;
      if (state.attempted[key] === cookieDigest(cookie)) continue;
      plan.set(key, cookie);
    }
    for (const [id, taken] of forced) {
      if (id === browser.id) continue;
      for (const { site, cookies } of taken) {
        if (state.blocked[site] != null) continue;
        for (const [key, cookie] of cookies) {
          if (differs(key, cookie)) plan.set(key, cookie);
        }
      }
    }
    for (const [key, digest] of Object.entries(state.attempted)) {
      const held = next.get(key);
      if (held == null || cookieDigest(held) !== digest) delete state.attempted[key];
    }
    if (plan.size > 0) pushes.set(browser.id, [...plan.values()]);
  }

  return { canonical: next, pushes, states, events };
}
