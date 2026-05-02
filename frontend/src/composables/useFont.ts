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

function nsKey(suffix: string): string {
  return `WeKnora_${readUserId()}_${suffix}`
}

function loadSans(): FontKey {
  const v = localStorage.getItem(nsKey(SANS_KEY))
  return isFontKey(v) ? v : DEFAULT_SANS
}

function loadMono(): MonoFontKey {
  const v = localStorage.getItem(nsKey(MONO_KEY))
  return isMonoFontKey(v) ? v : DEFAULT_MONO
}

function loadSize(): FontSizeKey {
  const v = localStorage.getItem(nsKey(SIZE_KEY))
  return isFontSizeKey(v) ? v : DEFAULT_SIZE
}

const currentSans = ref<FontKey>(loadSans())
const currentMono = ref<MonoFontKey>(loadMono())
const currentSize = ref<FontSizeKey>(loadSize())

function applyFont() {
  const root = document.documentElement
  root.style.setProperty('--app-font-family', SANS_STACKS[currentSans.value])
  root.style.setProperty('--app-font-family-mono', MONO_STACKS[currentMono.value])
  root.style.setProperty('--app-font-scale', String(FONT_SCALES[currentSize.value]))
}

export function useFont() {
  function setSansFont(key: FontKey) {
    currentSans.value = key
    localStorage.setItem(nsKey(SANS_KEY), key)
    applyFont()
  }

  function setMonoFont(key: MonoFontKey) {
    currentMono.value = key
    localStorage.setItem(nsKey(MONO_KEY), key)
    applyFont()
  }

  function setFontSize(key: FontSizeKey) {
    currentSize.value = key
    localStorage.setItem(nsKey(SIZE_KEY), key)
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
