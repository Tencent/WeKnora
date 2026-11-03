// docker/scripts/wk-secret-fill.mjs
// Types credential values into the session browser for browser_fill_secret.
// Every page read runs in an isolated world: page scripts can redefine
// activeElement, type or value getters in the main world, not there.
import { createDecipheriv } from "node:crypto";
import { readFileSync, unlinkSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { pathToFileURL } from "node:url";
import { connectBrowser, readBrowserRootEndpoint } from "./cdp-cookies.mjs";

export const STATUS = Object.freeze({
  filled: "filled",
  hostMismatch: "host_mismatch",
  insecureOrigin: "insecure_origin",
  refNotFound: "ref_not_found",
  notEditable: "not_editable",
  notPasswordField: "not_password_field",
  pageChanged: "page_changed",
  verifyFailed: "verify_failed",
});

const WORLD = "wk-secret-fill";
const FOCUS_TIMEOUT_MS = 10000;
const NON_TEXT_INPUTS = ["hidden", "checkbox", "radio", "submit", "button", "image", "file", "reset", "range", "color"];

export const ACTIVE_FIELD = `(() => {
  const el = document.activeElement;
  if (!el || el === document.body) return { kind: "none" };
  if (el.tagName === "IFRAME" || el.tagName === "FRAME") return { kind: "frame" };
  const type = (el.getAttribute("type") || el.type || "").toLowerCase();
  const editable = (el.tagName === "INPUT" && !${JSON.stringify(NON_TEXT_INPUTS)}.includes(type)) || el.tagName === "TEXTAREA";
  if (!editable || el.disabled || el.readOnly) return { kind: "other" };
  return { kind: "field", type };
})()`;
export const SELECT_FIELD = `(() => { const el = document.activeElement; if (el && typeof el.select === "function") el.select(); return true; })()`;
export const VALUE_LENGTH = `(() => { const el = document.activeElement; return el && typeof el.value === "string" ? el.value.length : -1; })()`;
export const CLEAR_FIELD = `(() => { const el = document.activeElement; if (el && "value" in el) { el.value = ""; el.dispatchEvent(new Event("input", { bubbles: true })); } return true; })()`;

export function readKeyFile(path) {
  try {
    return Buffer.from(readFileSync(path, "utf8").trim(), "base64");
  } finally {
    try { unlinkSync(path); } catch {}
  }
}

export function decryptPayload(key, sealedB64) {
  const raw = Buffer.from(sealedB64, "base64");
  if (key.length !== 32 || raw.length < 12 + 16) throw new Error("decrypt_failed");
  const nonce = raw.subarray(0, 12);
  const tag = raw.subarray(raw.length - 16);
  const body = raw.subarray(12, raw.length - 16);
  const decipher = createDecipheriv("aes-256-gcm", key, nonce);
  decipher.setAuthTag(tag);
  const plain = Buffer.concat([decipher.update(body), decipher.final()]);
  try {
    return JSON.parse(plain.toString("utf8"));
  } finally {
    plain.fill(0);
  }
}

export function originStatus(url, domains) {
  let u;
  try { u = new URL(url); } catch { return STATUS.hostMismatch; }
  const host = u.hostname.toLowerCase().replace(/\.$/, "").replace(/^\[|\]$/g, "");
  if (!domains.includes(host)) return STATUS.hostMismatch;
  if (u.protocol !== "https:") return STATUS.insecureOrigin;
  return null;
}

async function openPage(browser, targetId) {
  const { sessionId } = await browser.send("Target.attachToTarget", { targetId, flatten: true });
  const mainFrame = async () => {
    const { frameTree } = await browser.send("Page.getFrameTree", {}, sessionId);
    return { id: frameTree.frame.id, url: frameTree.frame.url, loaderId: frameTree.frame.loaderId };
  };
  return {
    mainFrame,
    async evaluate(expression) {
      const frame = await mainFrame();
      const { executionContextId } = await browser.send(
        "Page.createIsolatedWorld", { frameId: frame.id, worldName: WORLD }, sessionId);
      const r = await browser.send(
        "Runtime.evaluate", { expression, contextId: executionContextId, returnByValue: true }, sessionId);
      if (r?.exceptionDetails) throw new Error("evaluate_failed");
      return r?.result?.value;
    },
    async insertText(text) { await browser.send("Input.insertText", { text }, sessionId); },
    async close() { await browser.send("Target.detachFromTarget", { sessionId }).catch(() => {}); },
  };
}

// agent-browser normally drives one tab. With several, prefer the visible one;
// a wrong pick only means the focus check fails and nothing is typed.
export async function choosePage(browser, open = openPage) {
  const { targetInfos } = await browser.send("Target.getTargets");
  const pages = (targetInfos ?? []).filter((t) => t.type === "page" && /^https?:/i.test(t.url ?? ""));
  if (pages.length === 0) return null;
  if (pages.length === 1) return open(browser, pages[0].targetId);
  for (const t of pages) {
    const page = await open(browser, t.targetId);
    const visible = await page.evaluate(`document.visibilityState === "visible"`).catch(() => false);
    if (visible) return page;
    await page.close();
  }
  return open(browser, pages[0].targetId);
}

export function focusRef(ref, wrapper = process.env.WK_AB_WRAPPER ?? "/usr/local/bin/agent-browser") {
  const env = { ...process.env, WK_AB_LOCK_HELD: "1" };
  for (const command of ["focus", "click"]) {
    const r = spawnSync(wrapper, [command, ref], { env, stdio: "ignore", timeout: FOCUS_TIMEOUT_MS });
    if (r.status === 0) return true;
  }
  return false;
}

// pageStatus reports the outcome of a document swap since before: a foreign or
// insecure origin keeps its own status, any other new document is
// page_changed. Same-document navigations keep the loaderId.
function pageStatus(before, now, domains) {
  const origin = originStatus(now.url, domains);
  if (origin) return origin;
  return now.id === before.id && now.loaderId === before.loaderId ? null : STATUS.pageChanged;
}

export async function fillFields({ payload, page, focus }) {
  const result = {};
  let stop = null;
  for (const field of payload.fields) {
    if (stop) { result[field.name] = stop; continue; }
    if (!focus(field.ref)) { result[field.name] = STATUS.refNotFound; continue; }
    const before = await page.mainFrame();
    const origin = originStatus(before.url, payload.domains);
    if (origin) { result[field.name] = origin; stop = origin; continue; }
    const active = await page.evaluate(ACTIVE_FIELD);
    if (active?.kind !== "field") { result[field.name] = STATUS.notEditable; continue; }
    if (field.type === "password" && active.type !== "password") {
      result[field.name] = STATUS.notPasswordField;
      continue;
    }
    await page.evaluate(SELECT_FIELD);
    // Re-read right before typing so a navigation during the checks above
    // never receives the value.
    const ready = pageStatus(before, await page.mainFrame(), payload.domains);
    if (ready) { result[field.name] = ready; stop = ready; continue; }
    await page.insertText(field.value);
    const after = pageStatus(before, await page.mainFrame(), payload.domains);
    if (after) { result[field.name] = after; stop = after; continue; }
    const length = await page.evaluate(VALUE_LENGTH);
    if (length !== field.value.length) {
      await page.evaluate(CLEAR_FIELD);
      result[field.name] = STATUS.verifyFailed;
      continue;
    }
    result[field.name] = STATUS.filled;
  }
  return result;
}

const print = (o) => process.stdout.write(`${JSON.stringify(o)}\n`);

export async function main(argv, {
  endpoint = readBrowserRootEndpoint,
  connect = connectBrowser,
  choose = choosePage,
  focus = focusRef,
  out = print,
} = {}) {
  const root = process.env.WEKNORA_BROWSER_ROOT;
  if (argv[0] === "--probe") {
    let browser;
    try {
      if (!root) return out({ url: null });
      browser = await connect(endpoint(root).port);
      const page = await choose(browser);
      if (!page) return out({ url: null });
      const frame = await page.mainFrame();
      await page.close();
      return out({ url: frame.url });
    } catch {
      return out({ url: null });
    } finally {
      browser?.close();
    }
  }
  if (argv[0] !== "--key-file" || argv.length !== 3) {
    out({ error: "usage" });
    return 2;
  }
  let payload;
  let key;
  try {
    key = readKeyFile(argv[1]);
    payload = decryptPayload(key, argv[2]);
  } catch {
    return out({ error: "decrypt_failed" });
  } finally {
    key?.fill(0);
  }
  let browser;
  try {
    if (!root) return out({ error: "no_browser" });
    browser = await connect(endpoint(root).port);
    const page = await choose(browser);
    if (!page) {
      return out({ fields: Object.fromEntries(payload.fields.map((f) => [f.name, STATUS.notEditable])) });
    }
    const fields = await fillFields({ payload, page, focus });
    await page.close();
    return out({ fields });
  } catch {
    return out({ error: "no_browser" });
  } finally {
    for (const f of payload.fields) f.value = "";
    browser?.close();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const code = await main(process.argv.slice(2));
  process.exitCode = typeof code === "number" ? code : 0;
}
