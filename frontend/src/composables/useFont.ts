import { ref } from 'vue'
import {
  loadPreference,
  savePreference,
  migratePreferencesIntoUser,
} from './preferenceStorage'

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

function loadSans(): FontKey {
  const v = loadPreference(SANS_KEY)
  return isFontKey(v) ? v : DEFAULT_SANS
}

function loadMono(): MonoFontKey {
  const v = loadPreference(MONO_KEY)
  return isMonoFontKey(v) ? v : DEFAULT_MONO
}

function loadSize(): FontSizeKey {
  const v = loadPreference(SIZE_KEY)
  return isFontSizeKey(v) ? v : DEFAULT_SIZE
}

const currentSans = ref<FontKey>(loadSans())
const currentMono = ref<MonoFontKey>(loadMono())
const currentSize = ref<FontSizeKey>(loadSize())

// Track the last value applied to the DOM so we only rewrite CSS variables
// that actually changed. Avoids unnecessary style recalculation when the
// user only flips one of the three knobs.
const lastApplied: { sans: string; mono: string; scale: string } = {
  sans: '',
  mono: '',
  scale: '',
}

function applyFont() {
  const root = document.documentElement
  if (!root) return
  const sansStack = SANS_STACKS[currentSans.value] ?? SANS_STACKS[DEFAULT_SANS]
  const monoStack = MONO_STACKS[currentMono.value] ?? MONO_STACKS[DEFAULT_MONO]
  const scale = String(FONT_SCALES[currentSize.value] ?? FONT_SCALES[DEFAULT_SIZE])
  if (lastApplied.sans !== sansStack) {
    root.style.setProperty('--app-font-family', sansStack)
    lastApplied.sans = sansStack
  }
  if (lastApplied.mono !== monoStack) {
    root.style.setProperty('--app-font-family-mono', monoStack)
    lastApplied.mono = monoStack
  }
  if (lastApplied.scale !== scale) {
    root.style.setProperty('--app-font-scale', scale)
    lastApplied.scale = scale
  }
}

export function useFont() {
  function setSansFont(key: FontKey): boolean {
    if (!isFontKey(key)) return false
    currentSans.value = key
    savePreference(SANS_KEY, key)
    applyFont()
    return true
  }

  function setMonoFont(key: MonoFontKey): boolean {
    if (!isMonoFontKey(key)) return false
    currentMono.value = key
    savePreference(MONO_KEY, key)
    applyFont()
    return true
  }

  function setFontSize(key: FontSizeKey): boolean {
    if (!isFontSizeKey(key)) return false
    currentSize.value = key
    savePreference(SIZE_KEY, key)
    applyFont()
    return true
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
  migratePreferencesIntoUser()
  currentSans.value = loadSans()
  currentMono.value = loadMono()
  currentSize.value = loadSize()
  applyFont()
}
