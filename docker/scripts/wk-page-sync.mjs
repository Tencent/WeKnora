import {
  attachToPage,
  detachSession,
  hostOfOrigin,
  listPageTargets,
  readPageStorage,
  reloadPage,
  setPageStorage,
} from "./cdp-cookies.mjs";
import { siteOf } from "./wk-cookie-plan.mjs";

export const RELOAD_WINDOW_MS = 60_000;
export const RELOAD_MAX_PER_WINDOW = 3;
export const RELOAD_QUIET_MS = 20_000;

const STORAGE_SEP = "\u0000";
export function makeStorageKey(origin, key) {
  return `${origin}${STORAGE_SEP}${key}`;
}
export function splitStorageKey(storageKey) {
  const i = storageKey.indexOf(STORAGE_SEP);
  return [storageKey.slice(0, i), storageKey.slice(i + 1)];
}

export function mergeStorage(perBrowser) {
  const canonical = new Map();
  for (const origins of perBrowser) {
    for (const [origin, items] of origins) {
      for (const [key, value] of items) {
        const storageKey = makeStorageKey(origin, key);
        const existing = canonical.get(storageKey);
        if (existing == null || (existing === "" && value !== "")) canonical.set(storageKey, value);
      }
    }
  }
  return canonical;
}

export function selectStorageSeed(origin, canonical, currentItems, offered) {
  const entries = [];
  const offeredKeys = [];
  for (const [storageKey, value] of canonical) {
    const [o, key] = splitStorageKey(storageKey);
    if (o !== origin || currentItems.has(key) || offered?.get(storageKey) === value) continue;
    entries.push([key, value]);
    offeredKeys.push([storageKey, value]);
  }
  return { entries, offeredKeys };
}

// A page whose storage never converges would otherwise be reloaded every pass.
export class ReloadBreaker {
  constructor(now = Date.now) {
    this.now = now;
    this.byId = new Map();
  }

  stateFor(id, host) {
    let perHost = this.byId.get(id);
    if (perHost == null) this.byId.set(id, (perHost = new Map()));
    let state = perHost.get(host);
    if (state == null) {
      perHost.set(host, (state = { issued: [], open: false, lastRequestAt: null, pending: false }));
    }
    return state;
  }

  forget(id) {
    this.byId.delete(id);
  }

  hasPending(ids) {
    return ids.some((id) => [...(this.byId.get(id)?.values() ?? [])].some((s) => s.open && s.pending));
  }

  request(id, host) {
    const t = this.now();
    const state = this.stateFor(id, host);
    const quietForMs = state.lastRequestAt == null ? Infinity : t - state.lastRequestAt;
    state.lastRequestAt = t;
    if (state.open) {
      if (quietForMs < RELOAD_QUIET_MS) {
        state.pending = true;
        return "open";
      }
      Object.assign(state, { open: false, pending: false, issued: [t] });
      return "allow-after-quiet";
    }
    state.issued = state.issued.filter((at) => t - at < RELOAD_WINDOW_MS);
    if (state.issued.length >= RELOAD_MAX_PER_WINDOW) {
      state.open = true;
      return "trip";
    }
    state.issued.push(t);
    return "allow";
  }

  takeDue(availableById) {
    const t = this.now();
    const due = [];
    for (const [id, perHost] of this.byId) {
      for (const [host, state] of perHost) {
        if (!state.open || !state.pending) continue;
        if (state.lastRequestAt != null && t - state.lastRequestAt < RELOAD_QUIET_MS) continue;
        if (!availableById.get(id)?.has(host)) continue;
        Object.assign(state, { open: false, pending: false, issued: [t] });
        due.push({ id, host });
      }
    }
    return due;
  }
}

/**
 * members: [{ id, browserId, browser, blocked: Set<site>, busy: boolean }]
 * reloadSites: Map<id, Set<site>> — sites whose login cookies just changed
 * in that browser.
 */
export class PageSync {
  constructor({ now = Date.now, log = () => {} } = {}) {
    this.log = log;
    this.breaker = new ReloadBreaker(now);
    this.storageOffered = new Map();
    this.withheld = new Map();
    this.browserIds = new Map();
  }

  forgetReplaced(members) {
    for (const { id, browserId } of members) {
      const prev = this.browserIds.get(id);
      if (prev != null && prev !== browserId) {
        this.storageOffered.delete(id);
        this.withheld.delete(id);
        this.breaker.forget(id);
      }
      this.browserIds.set(id, browserId);
    }
  }

  async run(members, reloadSites = new Map()) {
    this.forgetReplaced(members);
    const ids = members.map((m) => m.id);
    const anyReload = [...reloadSites.values()].some((sites) => sites.size > 0);
    if (members.length < 2 && !anyReload && !this.breaker.hasPending(ids) &&
        !ids.some((id) => this.withheld.has(id))) {
      return;
    }
    const pagesById = await this.attachPages(members);
    try {
      const storageHosts = members.length >= 2 ? await this.mirrorStorage(members, pagesById) : new Map();
      await this.reloadReceivers(members, pagesById, reloadSites, storageHosts);
      await this.deliverDeferred(members, pagesById);
      await this.deliverWithheld(members, pagesById);
    } finally {
      for (const pages of pagesById.values()) {
        for (const page of pages) await detachSession(page.browser, page.sessionId);
      }
    }
  }

  async attachPages(members) {
    const byId = new Map();
    for (const { id, browser } of members) {
      const pages = [];
      let targets = [];
      try {
        targets = await listPageTargets(browser);
      } catch {}
      for (const { targetId } of targets) {
        const sessionId = await attachToPage(browser, targetId);
        if (sessionId == null) continue;
        const storage = await readPageStorage(browser, sessionId);
        if (storage == null) {
          await detachSession(browser, sessionId);
          continue;
        }
        pages.push({ browser, sessionId, origin: storage.origin, host: hostOfOrigin(storage.origin), items: storage.items });
      }
      byId.set(id, pages);
    }
    return byId;
  }

  async mirrorStorage(members, pagesById) {
    const originsById = new Map();
    for (const { id, blocked } of members) {
      const origins = new Map();
      for (const page of pagesById.get(id) ?? []) {
        if (blocked.has(siteOf(page.host))) continue;
        if (!origins.has(page.origin)) origins.set(page.origin, new Map());
        for (const [key, value] of page.items) origins.get(page.origin).set(key, value);
      }
      originsById.set(id, origins);
    }
    const canonical = mergeStorage([...originsById.values()]);
    const hostsById = new Map();
    let synced = 0;
    for (const [id, origins] of originsById) {
      let offered = this.storageOffered.get(id);
      if (offered == null) this.storageOffered.set(id, (offered = new Map()));
      for (const [origin, items] of origins) {
        const { entries, offeredKeys } = selectStorageSeed(origin, canonical, items, offered);
        if (entries.length === 0) continue;
        const page = pagesById.get(id).find((p) => p.origin === origin);
        await setPageStorage(page.browser, page.sessionId, entries);
        for (const [storageKey, value] of offeredKeys) offered.set(storageKey, value);
        synced += entries.length;
        if (!hostsById.has(id)) hostsById.set(id, new Set());
        hostsById.get(id).add(page.host);
      }
    }
    if (synced > 0) this.log(`mirrored ${synced} localStorage entr${synced === 1 ? "y" : "ies"}`);
    return hostsById;
  }

  requestReload(id, host) {
    const status = this.breaker.request(id, host);
    if (status === "trip") {
      this.log(`reload breaker open for ${host} in ${id}; cookies still sync, reloads paused`);
    }
    return status === "allow" || status === "allow-after-quiet";
  }

  async reloadReceivers(members, pagesById, reloadSites, storageHosts) {
    for (const { id, busy } of members) {
      const sites = reloadSites.get(id) ?? new Set();
      const hosts = storageHosts.get(id) ?? new Set();
      const decided = new Map();
      for (const page of pagesById.get(id) ?? []) {
        if (!sites.has(siteOf(page.host)) && !hosts.has(page.host)) continue;
        if (!decided.has(page.host)) {
          if (busy) {
            if (!this.withheld.has(id)) this.withheld.set(id, new Set());
            this.withheld.get(id).add(page.host);
            decided.set(page.host, false);
          } else {
            decided.set(page.host, this.requestReload(id, page.host));
          }
        }
        if (decided.get(page.host)) await reloadPage(page.browser, page.sessionId);
      }
    }
  }

  async deliverDeferred(members, pagesById) {
    const available = new Map();
    for (const { id, busy } of members) {
      if (!busy) available.set(id, new Set((pagesById.get(id) ?? []).map((p) => p.host)));
    }
    for (const { id, host } of this.breaker.takeDue(available)) {
      for (const page of pagesById.get(id) ?? []) {
        if (page.host === host) await reloadPage(page.browser, page.sessionId);
      }
    }
  }

  async deliverWithheld(members, pagesById) {
    for (const { id, busy } of members) {
      const hosts = this.withheld.get(id);
      if (hosts == null || busy || (pagesById.get(id) ?? []).length === 0) continue;
      this.withheld.delete(id);
      for (const host of hosts) {
        if (!this.requestReload(id, host)) continue;
        for (const page of pagesById.get(id)) {
          if (page.host === host) await reloadPage(page.browser, page.sessionId);
        }
      }
    }
  }
}
