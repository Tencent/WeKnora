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

const SANS_STORAGE_KEY = 'WeKnora_font_sans'
const MONO_STORAGE_KEY = 'WeKnora_font_mono'

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

const DEFAULT_SANS: FontKey = 'system'
const DEFAULT_MONO: MonoFontKey = 'system'

const isFontKey = (v: string | null): v is FontKey =>
  !!v && Object.prototype.hasOwnProperty.call(SANS_STACKS, v)

const isMonoFontKey = (v: string | null): v is MonoFontKey =>
  !!v && Object.prototype.hasOwnProperty.call(MONO_STACKS, v)

const currentSans = ref<FontKey>(
  isFontKey(localStorage.getItem(SANS_STORAGE_KEY))
    ? (localStorage.getItem(SANS_STORAGE_KEY) as FontKey)
    : DEFAULT_SANS,
)

const currentMono = ref<MonoFontKey>(
  isMonoFontKey(localStorage.getItem(MONO_STORAGE_KEY))
    ? (localStorage.getItem(MONO_STORAGE_KEY) as MonoFontKey)
    : DEFAULT_MONO,
)

function applyFont(sans: FontKey, mono: MonoFontKey) {
  const root = document.documentElement
  root.style.setProperty('--app-font-family', SANS_STACKS[sans])
  root.style.setProperty('--app-font-family-mono', MONO_STACKS[mono])
}

export function useFont() {
  function setSansFont(key: FontKey) {
    currentSans.value = key
    localStorage.setItem(SANS_STORAGE_KEY, key)
    applyFont(currentSans.value, currentMono.value)
  }

  function setMonoFont(key: MonoFontKey) {
    currentMono.value = key
    localStorage.setItem(MONO_STORAGE_KEY, key)
    applyFont(currentSans.value, currentMono.value)
  }

  return { currentSans, currentMono, setSansFont, setMonoFont }
}

/** Call once in main.ts to apply persisted font preferences before mount. */
export function initFont() {
  applyFont(currentSans.value, currentMono.value)
}
