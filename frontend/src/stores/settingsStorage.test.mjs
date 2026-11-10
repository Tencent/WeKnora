import assert from "node:assert/strict";
import test from "node:test";

const SETTINGS_STORAGE_KEY = "WeKnora_settings";
const BUILTIN_QUICK_ANSWER_ID = "builtin-quick-answer";
const BUILTIN_SMART_REASONING_ID = "builtin-smart-reasoning";

function reconcileBuiltinAgentMode(settings) {
  const agentId = settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID;
  if (agentId === BUILTIN_QUICK_ANSWER_ID && settings.isAgentEnabled) {
    settings.isAgentEnabled = false;
    return true;
  }
  if (agentId === BUILTIN_SMART_REASONING_ID && !settings.isAgentEnabled) {
    settings.isAgentEnabled = true;
    return true;
  }
  return false;
}

function cloneSettings(settings) {
  return JSON.parse(JSON.stringify(settings));
}

function isStoredSettingsRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function reconcileLoadedSettings(loaded) {
  loaded.selectedTags ||= [];
  loaded.selectedMCPServices ||= [];
  loaded.selectedSkills ||= loaded.selectedTools || [];
  loaded.selectedFileKbMap ||= {};
  if (reconcileBuiltinAgentMode(loaded)) {
    localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(loaded));
  }
  return loaded;
}

function resetStoredSettings(defaultSettings, reason) {
  localStorage.removeItem(SETTINGS_STORAGE_KEY);
  return reconcileLoadedSettings(cloneSettings(defaultSettings));
}

function loadAndReconcileSettings(defaultSettings) {
  try {
    const raw = localStorage.getItem(SETTINGS_STORAGE_KEY);
    if (!raw) {
      return reconcileLoadedSettings(cloneSettings(defaultSettings));
    }
    const parsed = JSON.parse(raw);
    if (!isStoredSettingsRecord(parsed)) {
      return resetStoredSettings(
        defaultSettings,
        new Error("stored value is not a settings object"),
      );
    }
    return reconcileLoadedSettings(parsed);
  } catch (e) {
    return resetStoredSettings(defaultSettings, e);
  }
}

function makeDefaults() {
  return {
    isAgentEnabled: false,
    selectedAgentId: BUILTIN_QUICK_ANSWER_ID,
    selectedTags: [],
    selectedMCPServices: [],
    selectedSkills: [],
    selectedFileKbMap: {},
    nested: { items: ["a"] },
  };
}

function installMockLocalStorage() {
  const store = {};
  Object.defineProperty(globalThis, "localStorage", {
    value: {
      getItem: (key) => (key in store ? store[key] : null),
      setItem: (key, value) => {
        store[key] = value;
      },
      removeItem: (key) => {
        delete store[key];
      },
    },
    configurable: true,
    writable: true,
  });
  return store;
}

test("isStoredSettingsRecord rejects non-object JSON values", () => {
  assert.equal(isStoredSettingsRecord(null), false);
  assert.equal(isStoredSettingsRecord([]), false);
  assert.equal(isStoredSettingsRecord("x"), false);
  assert.equal(isStoredSettingsRecord({}), true);
});

test("cloneSettings deep-clones nested structures", () => {
  const defaults = makeDefaults();
  const cloned = cloneSettings(defaults);
  cloned.nested.items.push("b");
  assert.deepEqual(defaults.nested.items, ["a"]);
});

test("loadAndReconcileSettings returns deep-cloned defaults when key is missing", () => {
  const store = installMockLocalStorage();
  const defaults = makeDefaults();

  const loaded = loadAndReconcileSettings(defaults);
  loaded.selectedTags.push("tag-1");

  assert.deepEqual(defaults.selectedTags, []);
  assert.equal(store[SETTINGS_STORAGE_KEY], undefined);
});

test("loadAndReconcileSettings resets invalid JSON and removes corrupted key", () => {
  const store = installMockLocalStorage();
  store[SETTINGS_STORAGE_KEY] = "{broken";

  const loaded = loadAndReconcileSettings(makeDefaults());

  assert.equal(store[SETTINGS_STORAGE_KEY], undefined);
  assert.deepEqual(loaded.selectedTags, []);
});

test("loadAndReconcileSettings resets non-object JSON such as null", () => {
  const store = installMockLocalStorage();
  store[SETTINGS_STORAGE_KEY] = "null";

  const loaded = loadAndReconcileSettings(makeDefaults());

  assert.equal(store[SETTINGS_STORAGE_KEY], undefined);
  assert.deepEqual(loaded.selectedTags, []);
});

test("loadAndReconcileSettings keeps valid stored settings", () => {
  const store = installMockLocalStorage();
  const stored = {
    isAgentEnabled: true,
    selectedAgentId: BUILTIN_QUICK_ANSWER_ID,
    selectedTags: [{ id: "t1", name: "Tag", kbId: "kb1" }],
  };
  store[SETTINGS_STORAGE_KEY] = JSON.stringify(stored);

  const loaded = loadAndReconcileSettings(makeDefaults());

  assert.equal(loaded.isAgentEnabled, false);
  assert.equal(loaded.selectedTags.length, 1);
  assert.equal(loaded.selectedTags[0].id, "t1");
});
