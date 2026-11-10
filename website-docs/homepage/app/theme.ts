// VitePress uses this same key and the values light / dark / auto.
export const themeStorageKey = "vitepress-theme-appearance";

export const themeInitializationScript = `(function(){var t='auto';try{t=localStorage.getItem('${themeStorageKey}')||'auto'}catch(e){}document.documentElement.classList.toggle('dark',t==='dark'||(t!=='light'&&matchMedia('(prefers-color-scheme: dark)').matches))})()`;

export function getThemeSnapshot() {
  return document.documentElement.classList.contains("dark");
}

export function subscribeToTheme(notify: () => void) {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const applyPreference = () => {
    let preference = "auto";
    try { preference = localStorage.getItem(themeStorageKey) || "auto"; } catch { /* Storage can be disabled. */ }
    document.documentElement.classList.toggle("dark", preference === "dark" || (preference !== "light" && media.matches));
    notify();
  };
  const onStorage = (event: StorageEvent) => {
    if (event.key === themeStorageKey || event.key === null) applyPreference();
  };
  const observer = new MutationObserver(notify);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  media.addEventListener("change", applyPreference);
  window.addEventListener("storage", onStorage);
  window.addEventListener("pageshow", applyPreference);
  applyPreference();
  return () => {
    observer.disconnect();
    media.removeEventListener("change", applyPreference);
    window.removeEventListener("storage", onStorage);
    window.removeEventListener("pageshow", applyPreference);
  };
}

export function toggleTheme() {
  const dark = !getThemeSnapshot();
  document.documentElement.classList.toggle("dark", dark);
  try { localStorage.setItem(themeStorageKey, dark ? "dark" : "light"); } catch { /* The current page still switches. */ }
}
