import { ref, watch } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'WeKnora_theme'

// Shared reactive state across all consumers
const currentTheme = ref<ThemeMode>(
  (localStorage.getItem(STORAGE_KEY) as ThemeMode) || 'light'
)

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
    currentTheme.value = mode
    localStorage.setItem(STORAGE_KEY, mode)
    applyTheme(mode)
  }

  return { currentTheme, setTheme }
}

/** Call once in main.ts to initialise theme and listen for OS changes. */
export function initTheme() {
  const saved = (localStorage.getItem(STORAGE_KEY) as ThemeMode) || 'light'
  currentTheme.value = saved
  applyTheme(saved)

  // React to OS theme changes when user chose "system"
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (currentTheme.value === 'system') {
      applyTheme('system')
    }
  })
}
