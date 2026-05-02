import { ref } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const THEME_KEY = 'theme'

function readUserId(): string {
  try {
    const raw = localStorage.getItem('weknora_user')
    if (!raw) return 'anon'
    const parsed = JSON.parse(raw)
    return parsed?.id ? String(parsed.id) : 'anon'
  } catch {
    return 'anon'
  }
}

function safeGetItem(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function safeSetItem(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Quota / disabled storage / private mode — preference applies to this session only.
  }
}

/**
 * Look up a stored preference, falling back through:
 *   1. The current user's namespace
 *   2. The "anon" namespace (settings chosen on the login screen)
 *   3. A legacy un-namespaced key (from earlier versions of this branch)
 */
function resolveStorageValue(suffix: string): string | null {
  const userId = readUserId()
  const userValue = safeGetItem(`WeKnora_${userId}_${suffix}`)
  if (userValue !== null) return userValue
  if (userId !== 'anon') {
    const anonValue = safeGetItem(`WeKnora_anon_${suffix}`)
    if (anonValue !== null) return anonValue
  }
  return safeGetItem(`WeKnora_${suffix}`)
}

function nsKey(): string {
  return `WeKnora_${readUserId()}_${THEME_KEY}`
}

function loadTheme(): ThemeMode {
  const v = resolveStorageValue(THEME_KEY)
  if (v === 'light' || v === 'dark' || v === 'system') return v
  return 'light'
}

// Shared reactive state across all consumers
const currentTheme = ref<ThemeMode>(loadTheme())

function getSystemTheme(): 'light' | 'dark' {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/** Wails：与原生窗口底色 / 系统深浅色一致，减轻 Ctrl+R 整窗白闪（浅色与 --td-bg-color-page #eee 对齐） */
function syncWailsNativeChrome(effective: 'light' | 'dark') {
  const bg = effective === 'dark' ? '#181818' : '#eeeeee'
  document.documentElement.style.background = bg
  document.documentElement.style.minHeight = '100%'
  document.documentElement.style.colorScheme = effective === 'dark' ? 'dark' : 'light'
  if (document.body) {
    document.body.style.background = bg
    document.body.style.minHeight = '100%'
  }
  const appEl = document.getElementById('app')
  if (appEl) {
    appEl.style.background = bg
    appEl.style.minHeight = '100%'
  }
  const w = (window as unknown as {
    runtime?: {
      WindowSetDarkTheme?: () => void
      WindowSetLightTheme?: () => void
      WindowSetBackgroundColour?: (r: number, g: number, b: number, a: number) => void
    }
  }).runtime
  if (!w?.WindowSetBackgroundColour) return
  try {
    if (effective === 'dark') {
      w.WindowSetDarkTheme?.()
      w.WindowSetBackgroundColour(24, 24, 24, 255)
    } else {
      w.WindowSetLightTheme?.()
      w.WindowSetBackgroundColour(238, 238, 238, 255)
    }
  } catch {
    /* 非桌面壳或未注入 runtime */
  }
}

function applyTheme(mode: ThemeMode) {
  const effective = mode === 'system' ? getSystemTheme() : mode
  document.documentElement.setAttribute('theme-mode', effective)
  syncWailsNativeChrome(effective)
}

export function useTheme() {
  function setTheme(mode: ThemeMode) {
    if (mode !== 'light' && mode !== 'dark' && mode !== 'system') return
    currentTheme.value = mode
    safeSetItem(nsKey(), mode)
    applyTheme(mode)
  }

  return { currentTheme, setTheme }
}

/** Call once in main.ts to initialise theme and listen for OS changes. */
export function initTheme() {
  currentTheme.value = loadTheme()
  applyTheme(currentTheme.value)

  // React to OS theme changes when user chose "system"
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (currentTheme.value === 'system') {
      applyTheme('system')
    }
  })
}

/** Re-read preferences from storage (call after login / logout). */
export function reloadThemeFromStorage() {
  currentTheme.value = loadTheme()
  applyTheme(currentTheme.value)
}
