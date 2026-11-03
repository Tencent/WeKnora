// docker/scripts/tests/test_secret_fill.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { createCipheriv, randomBytes } from "node:crypto";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  ACTIVE_FIELD, CLEAR_FIELD, SELECT_FIELD, STATUS, VALUE_LENGTH,
  decryptPayload, fillFields, main, originStatus, readKeyFile,
} from "../wk-secret-fill.mjs";

// Same layout as Go's sealSecretPayload: nonce(12) || ciphertext || tag(16).
function seal(key, obj) {
  const nonce = randomBytes(12);
  const c = createCipheriv("aes-256-gcm", key, nonce);
  const body = Buffer.concat([c.update(JSON.stringify(obj), "utf8"), c.final()]);
  return Buffer.concat([nonce, body, c.getAuthTag()]).toString("base64");
}

function fakePage({
  url = "https://accounts.google.com/signin", active = { kind: "field", type: "password" }, keeps = true,
  navigateTo, navigateOnSelect,
} = {}) {
  const state = { url, loaderId: "L1", value: "", typed: [], cleared: false };
  const navigate = (to) => { state.url = to; state.loaderId = `L${Number(state.loaderId.slice(1)) + 1}`; };
  return {
    state,
    async mainFrame() { return { id: "F1", url: state.url, loaderId: state.loaderId }; },
    async evaluate(expr) {
      if (expr === ACTIVE_FIELD) return active;
      if (expr === SELECT_FIELD) {
        state.value = "";
        if (navigateOnSelect) navigate(navigateOnSelect);
        return true;
      }
      if (expr === VALUE_LENGTH) return state.value.length;
      if (expr === CLEAR_FIELD) { state.value = ""; state.cleared = true; return true; }
      throw new Error(`unexpected expression ${expr}`);
    },
    async insertText(text) {
      state.typed.push(text);
      if (keeps) state.value += text;
      if (navigateTo) navigate(navigateTo);
    },
    async close() {},
  };
}

const payload = (fields, domains = ["accounts.google.com"]) => ({ domains, fields });

test("decryptPayload opens what Go seals and rejects tampering", () => {
  const key = randomBytes(32);
  const sealed = seal(key, payload([{ name: "password", ref: "@e3", type: "password", value: "pw" }]));
  assert.equal(decryptPayload(key, sealed).fields[0].value, "pw");
  const raw = Buffer.from(sealed, "base64");
  raw[20] ^= 1;
  assert.throws(() => decryptPayload(key, raw.toString("base64")));
  assert.throws(() => decryptPayload(randomBytes(32), sealed));
});

test("readKeyFile deletes the key file even when it is unreadable", () => {
  const dir = mkdtempSync(join(tmpdir(), "wk-fill-"));
  try {
    const path = join(dir, "k");
    writeFileSync(path, randomBytes(32).toString("base64"));
    assert.equal(readKeyFile(path).length, 32);
    assert.equal(existsSync(path), false);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("originStatus checks host and scheme", () => {
  const d = ["accounts.google.com", "::1"];
  assert.equal(originStatus("https://accounts.google.com/x", d), null);
  assert.equal(originStatus("https://ACCOUNTS.google.com./x", d), null);
  assert.equal(originStatus("http://accounts.google.com/x", d), STATUS.insecureOrigin);
  assert.equal(originStatus("https://evil.com/x", d), STATUS.hostMismatch);
  assert.equal(originStatus("https://[::1]:8443/", d), null);
  assert.equal(originStatus("not a url", d), STATUS.hostMismatch);
});

test("fillFields fills, verifies and reports per field", async () => {
  const page = fakePage();
  const focused = [];
  const result = await fillFields({
    payload: payload([{ name: "password", ref: "@e3", type: "password", value: "pw-Δ" }]),
    page, focus: (ref) => { focused.push(ref); return true; },
  });
  assert.deepEqual(result, { password: STATUS.filled });
  assert.deepEqual(focused, ["@e3"]);
  assert.deepEqual(page.state.typed, ["pw-Δ"]);
});

test("fillFields refuses the wrong field kinds", async () => {
  const text = fakePage({ active: { kind: "field", type: "text" } });
  assert.deepEqual(await fillFields({ payload: payload([{ name: "password", ref: "@e1", type: "password", value: "x" }]),
    page: text, focus: () => true }), { password: STATUS.notPasswordField });
  assert.deepEqual(text.state.typed, []);

  const frame = fakePage({ active: { kind: "frame" } });
  assert.deepEqual(await fillFields({ payload: payload([{ name: "otp", ref: "@e1", type: "one_time_code", value: "1" }]),
    page: frame, focus: () => true }), { otp: STATUS.notEditable });

  const missing = fakePage();
  assert.deepEqual(await fillFields({ payload: payload([{ name: "otp", ref: "@e9", type: "text", value: "1" }]),
    page: missing, focus: () => false }), { otp: STATUS.refNotFound });
});

test("fillFields stops on a foreign or insecure page", async () => {
  const evil = fakePage({ url: "https://evil.com/login" });
  const result = await fillFields({
    payload: payload([
      { name: "username", ref: "@e2", type: "text", value: "alice" },
      { name: "password", ref: "@e3", type: "password", value: "pw" },
    ]),
    page: evil, focus: () => true,
  });
  assert.deepEqual(result, { username: STATUS.hostMismatch, password: STATUS.hostMismatch });
  assert.deepEqual(evil.state.typed, []);

  const http = fakePage({ url: "http://accounts.google.com/login" });
  assert.deepEqual(await fillFields({ payload: payload([{ name: "password", ref: "@e3", type: "password", value: "pw" }]),
    page: http, focus: () => true }), { password: STATUS.insecureOrigin });
});

test("fillFields stops when typing navigates away", async () => {
  const page = fakePage({ active: { kind: "field", type: "text" }, navigateTo: "https://evil.com/" });
  const result = await fillFields({
    payload: payload([
      { name: "username", ref: "@e2", type: "text", value: "alice" },
      { name: "otp", ref: "@e4", type: "text", value: "1" },
    ]),
    page, focus: () => true,
  });
  assert.deepEqual(result, { username: STATUS.hostMismatch, otp: STATUS.hostMismatch });
  assert.deepEqual(page.state.typed, ["alice"]);
});

test("fillFields types nothing when the page changes before typing", async () => {
  const foreign = fakePage({ navigateOnSelect: "https://evil.com/" });
  assert.deepEqual(await fillFields({ payload: payload([{ name: "password", ref: "@e3", type: "password", value: "pw" }]),
    page: foreign, focus: () => true }), { password: STATUS.hostMismatch });
  assert.deepEqual(foreign.state.typed, []);

  const sameSite = fakePage({ navigateOnSelect: "https://accounts.google.com/other" });
  assert.deepEqual(await fillFields({ payload: payload([
    { name: "password", ref: "@e3", type: "password", value: "pw" },
    { name: "otp", ref: "@e4", type: "text", value: "1" },
  ]), page: sameSite, focus: () => true }), { password: STATUS.pageChanged, otp: STATUS.pageChanged });
  assert.deepEqual(sameSite.state.typed, []);
});

test("fillFields stops when typing loads another document on the same site", async () => {
  const page = fakePage({ active: { kind: "field", type: "text" }, navigateTo: "https://accounts.google.com/next" });
  assert.deepEqual(await fillFields({ payload: payload([
    { name: "username", ref: "@e2", type: "text", value: "alice" },
    { name: "otp", ref: "@e4", type: "text", value: "1" },
  ]), page, focus: () => true }), { username: STATUS.pageChanged, otp: STATUS.pageChanged });
  assert.deepEqual(page.state.typed, ["alice"]);
});

test("fillFields clears an input that did not keep the value", async () => {
  const page = fakePage({ keeps: false });
  assert.deepEqual(await fillFields({ payload: payload([{ name: "password", ref: "@e3", type: "password", value: "pw" }]),
    page, focus: () => true }), { password: STATUS.verifyFailed });
  assert.equal(page.state.cleared, true);
});

test("main --probe reports the page URL or null", async () => {
  const outputs = [];
  const out = (o) => outputs.push(o);
  process.env.WEKNORA_BROWSER_ROOT = "/br";
  const browser = { close() {} };
  await main(["--probe"], { endpoint: () => ({ port: 1 }), connect: async () => browser,
    choose: async () => fakePage({ url: "https://a.com/x" }), out });
  await main(["--probe"], { endpoint: () => { throw new Error("no port file"); }, out });
  assert.deepEqual(outputs, [{ url: "https://a.com/x" }, { url: null }]);
});

test("main fill decrypts, fills and never prints a value", async () => {
  const dir = mkdtempSync(join(tmpdir(), "wk-fill-"));
  try {
    const key = randomBytes(32);
    const keyPath = join(dir, "k");
    writeFileSync(keyPath, key.toString("base64"));
    const sealed = seal(key, payload([{ name: "password", ref: "@e3", type: "password", value: "sentinel-pw" }]));
    const outputs = [];
    process.env.WEKNORA_BROWSER_ROOT = "/br";
    await main(["--key-file", keyPath, sealed], {
      endpoint: () => ({ port: 1 }), connect: async () => ({ close() {} }),
      choose: async () => fakePage(), focus: () => true, out: (o) => outputs.push(o),
    });
    assert.deepEqual(outputs, [{ fields: { password: STATUS.filled } }]);
    assert.equal(existsSync(keyPath), false, "the key file is gone");
    assert.ok(!JSON.stringify(outputs).includes("sentinel-pw"));

    const bad = [];
    writeFileSync(keyPath, randomBytes(32).toString("base64"));
    await main(["--key-file", keyPath, sealed], { out: (o) => bad.push(o) });
    assert.deepEqual(bad, [{ error: "decrypt_failed" }]);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
