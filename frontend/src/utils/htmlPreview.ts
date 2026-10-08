// This is a standalone script: it runs inside the opaque-origin iframe, never
// in the application window. Keep it independent of imports and bundler helpers.
// Each frame owns two ephemeral stores, discarded when the frame is reloaded.
const storageBootstrap = `<script>
(() => {
  function createStorage() {
    const entries = new Map();
    const api = Object.create(null);
    Object.defineProperties(api, {
      length: { configurable: true, get: () => entries.size },
      key: { configurable: true, value: (index) => Array.from(entries.keys())[Number(index) >>> 0] ?? null },
      getItem: { configurable: true, value: (key) => entries.get(String(key)) ?? null },
      setItem: { configurable: true, value: (key, value) => { entries.set(String(key), String(value)); } },
      removeItem: { configurable: true, value: (key) => { entries.delete(String(key)); } },
      clear: { configurable: true, value: () => { entries.clear(); } },
      [Symbol.toStringTag]: { configurable: true, value: 'Storage' },
    });
    return new Proxy(api, {
      get(target, key) {
        return Reflect.has(target, key) ? Reflect.get(target, key) : entries.get(key);
      },
      set(target, key, value) {
        if (typeof key === 'string') entries.set(key, String(value));
        return true;
      },
      deleteProperty(target, key) {
        entries.delete(key);
        return true;
      },
      has(target, key) {
        return Reflect.has(target, key) || entries.has(key);
      },
      ownKeys() {
        return Array.from(entries.keys());
      },
      getOwnPropertyDescriptor(target, key) {
        return Reflect.getOwnPropertyDescriptor(target, key) || (entries.has(key)
          ? { configurable: true, enumerable: true, writable: true, value: entries.get(key) }
          : undefined);
      },
    });
  }
  for (const name of ['localStorage', 'sessionStorage']) {
    try {
      // Leave working browser storage alone. Sandboxed opaque origins throw
      // here before a generated page can even register its event handlers.
      void window[name].length;
    } catch {
      const storage = createStorage();
      Object.defineProperty(window, name, { configurable: true, enumerable: true, get: () => storage });
    }
  }
})();
</script>`

/** Add compatibility only to the rendered copy, preserving the original source. */
export function buildHtmlPreview(html: string): string {
  // Keep the doctype first (standards mode), and use an existing head when
  // present. Only inspect the document prefix: text resembling tags inside
  // comments, scripts or the body must never become an insertion point.
  const trivia = String.raw`(?:\s|<!--[\s\S]*?-->)*`
  const attributes = String.raw`(?:[^<>"']|"[^"]*"|'[^']*')*`
  const prefix = new RegExp(
    `^${trivia}(?:<!doctype\\b${attributes}>${trivia})?(?:<html\\b${attributes}>${trivia})?(?:<head\\b${attributes}>)?`,
    'i',
  ).exec(html)?.[0] ?? ''
  return prefix + storageBootstrap + html.slice(prefix.length)
}
