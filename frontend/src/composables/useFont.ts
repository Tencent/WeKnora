import { ref } from 'vue'

export type FontKey =
  | 'system'
  | 'pingfang'
  | 'inter'
  | 'helvetica'
  | 'segoe'
  | 'roboto'
  | 'sans-serif'

export type MonoFontKey =
  | 'system'
  | 'cascadia'
  | 'jetbrains'
  | 'fira'
  | 'monaco'
  | 'consolas'
  | 'monospace'

export type FontSizeKey = 'small' | 'normal' | 'large'

const SANS_KEY = 'font_sans'
const MONO_KEY = 'font_mono'
const SIZE_KEY = 'font_size'

export const SANS_STACKS: Record<FontKey, string> = {
  system:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif',
  pingfang:
    '"PingFang SC", "Microsoft YaHei", "Hiragino Sans GB", -apple-system, BlinkMacSystemFont, sans-serif',
  inter:
    'Inter, "Segoe UI", -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif',
  helvetica:
    '"Helvetica Neue", Helvetica, Arial, "PingFang SC", "Microsoft YaHei", sans-serif',
  segoe:
    '"Segoe UI", Tahoma, Verdana, Arial, "PingFang SC", "Microsoft YaHei", sans-serif',
  roboto:
    'Roboto, "Segoe UI", -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", sans-serif',
  'sans-serif': 'sans-serif',
}

export const MONO_STACKS: Record<MonoFontKey, string> = {
  system:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
  cascadia:
    '"Cascadia Code", "Cascadia Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
  jetbrains:
    '"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
  fira: '"Fira Code", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
  monaco: 'Monaco, Menlo, Consolas, "Courier New", monospace',
  consolas: 'Consolas, "Courier New", Menlo, Monaco, monospace',
  monospace: 'monospace',
}

export const FONT_SCALES: Record<FontSizeKey, number> = {
  small: 0.875,
  normal: 1,
  large: 1.125,
}

const DEFAULT_SANS: FontKey = 'system'
const DEFAULT_MONO: MonoFontKey = 'system'
const DEFAULT_SIZE: FontSizeKey = 'normal'

const isFontKey = (v: string | null): v is FontKey =>
  !!v && Object.prototype.hasOwnProperty.call(SANS_STACKS, v)

const isMonoFontKey = (v: string | null): v is MonoFontKey =>
  !!v && Object.prototype.hasOwnProperty.call(MONO_STACKS, v)

const isFontSizeKey = (v: string | null): v is FontSizeKey =>
  v === 'small' || v === 'normal' || v === 'large'

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

function nsKey(suffix: string): string {
  return `WeKnora_${readUserId()}_${suffix}`
}

function loadSans(): FontKey {
  const v = resolveStorageValue(SANS_KEY)
  return isFontKey(v) ? v : DEFAULT_SANS
}

function loadMono(): MonoFontKey {
  const v = resolveStorageValue(MONO_KEY)
  return isMonoFontKey(v) ? v : DEFAULT_MONO
}

function loadSize(): FontSizeKey {
  const v = resolveStorageValue(SIZE_KEY)
  return isFontSizeKey(v) ? v : DEFAULT_SIZE
}

const currentSans = ref<FontKey>(loadSans())
const currentMono = ref<MonoFontKey>(loadMono())
const currentSize = ref<FontSizeKey>(loadSize())

function applyFont() {
  const root = document.documentElement
  if (!root) return
  const sansStack = SANS_STACKS[currentSans.value] ?? SANS_STACKS[DEFAULT_SANS]
  const monoStack = MONO_STACKS[currentMono.value] ?? MONO_STACKS[DEFAULT_MONO]
  const scale = FONT_SCALES[currentSize.value] ?? FONT_SCALES[DEFAULT_SIZE]
  root.style.setProperty('--app-font-family', sansStack)
  root.style.setProperty('--app-font-family-mono', monoStack)
  root.style.setProperty('--app-font-scale', String(scale))
}

export function useFont() {
  function setSansFont(key: FontKey) {
    if (!isFontKey(key)) return
    currentSans.value = key
    safeSetItem(nsKey(SANS_KEY), key)
    applyFont()
  }

  function setMonoFont(key: MonoFontKey) {
    if (!isMonoFontKey(key)) return
    currentMono.value = key
    safeSetItem(nsKey(MONO_KEY), key)
    applyFont()
  }

  function setFontSize(key: FontSizeKey) {
    if (!isFontSizeKey(key)) return
    currentSize.value = key
    safeSetItem(nsKey(SIZE_KEY), key)
    applyFont()
  }

  return {
    currentSans,
    currentMono,
    currentSize,
    setSansFont,
    setMonoFont,
    setFontSize,
  }
}

/** Call once in main.ts to apply persisted font preferences before mount. */
export function initFont() {
  applyFont()
}

/** Re-read preferences from storage (call after login / logout). */
export function reloadFontFromStorage() {
  currentSans.value = loadSans()
  currentMono.value = loadMono()
  currentSize.value = loadSize()
  applyFont()
}
