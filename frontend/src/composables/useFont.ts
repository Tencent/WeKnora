import { ref } from 'vue'
import {
  loadPreference,
  savePreference,
  migratePreferencesIntoUser,
} from './preferenceStorage'

// Font options are filtered by host platform. A Windows user selecting
// "PingFang SC" in a cross-platform list would see Microsoft YaHei anyway
// (fallback via the stack), so the labels would lie. Instead each OS
// surfaces the fonts it actually ships.
//   Mac:     system / pingfang / georgia  / sans-serif
//            system / menlo    / monaco   / monospace
//   Windows: system / yahei    / times    / sans-serif
//            system / consolas / cascadia / monospace
//   Linux:   system / noto-cjk / dejavu-serif / sans-serif
//            system / dejavu-mono / liberation-mono / monospace
//
// The "third" sans slot on each platform is a serif face (Georgia /
// Times New Roman / DejaVu Serif). This is intentional: on macOS the
// system sans, PingFang, and Helvetica are all visually close enough
// that users reported "the font doesn't change". A serif option produces
// a genuinely distinct look, so the setting feels responsive.
//
// All keys live in a single union so the persisted value is stable across
// devices; the runtime picker just hides the non-matching subset. A user
// who moves a localStorage value (e.g. "pingfang") to a Windows machine
// gets it validated against SANS_STACKS (which still contains the key) and
// the font stack still works — the browser just falls back to yahei on
// its own. Only truly unknown keys fall through to DEFAULT_SANS.
export type FontKey =
  | 'system'
  // Mac
  | 'pingfang'
  | 'georgia'
  // Windows
  | 'yahei'
  | 'times'
  // Linux
  | 'noto-cjk'
  | 'dejavu-serif'
  // cross-platform
  | 'sans-serif'

export type MonoFontKey =
  | 'system'
  // Mac
  | 'menlo'
  | 'monaco'
  // Windows
  | 'consolas'
  | 'cascadia'
  // Linux
  | 'dejavu-mono'
  | 'liberation-mono'
  // cross-platform
  | 'monospace'

export type FontSizeKey = 'small' | 'normal' | 'large'

const SANS_KEY = 'font_sans'
const MONO_KEY = 'font_mono'
const SIZE_KEY = 'font_size'

export const SANS_STACKS: Record<FontKey, string> = {
  system:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif',
  // Mac
  pingfang:
    '"PingFang SC", "Microsoft YaHei", "Hiragino Sans GB", -apple-system, BlinkMacSystemFont, sans-serif',
  georgia:
    'Georgia, "Times New Roman", "Songti SC", "SimSun", serif',
  // Windows
  yahei:
    '"Microsoft YaHei", "PingFang SC", "Hiragino Sans GB", Tahoma, Arial, sans-serif',
  times:
    '"Times New Roman", Times, Georgia, "SimSun", "Songti SC", serif',
  // Linux
  'noto-cjk':
    '"Noto Sans CJK SC", "Noto Sans SC", "Source Han Sans SC", "WenQuanYi Micro Hei", "Microsoft YaHei", sans-serif',
  'dejavu-serif':
    '"DejaVu Serif", "Liberation Serif", "Noto Serif", Georgia, "Times New Roman", serif',
  // cross-platform
  'sans-serif': 'sans-serif',
}

export const MONO_STACKS: Record<MonoFontKey, string> = {
  system:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
  // Mac
  menlo: 'Menlo, Monaco, Consolas, "Courier New", monospace',
  monaco: 'Monaco, Menlo, Consolas, "Courier New", monospace',
  // Windows
  consolas: 'Consolas, "Courier New", Menlo, Monaco, monospace',
  cascadia:
    '"Cascadia Code", "Cascadia Mono", Consolas, "Courier New", monospace',
  // Linux
  'dejavu-mono':
    '"DejaVu Sans Mono", "Liberation Mono", Menlo, Consolas, monospace',
  'liberation-mono':
    '"Liberation Mono", "DejaVu Sans Mono", Menlo, Consolas, monospace',
  // cross-platform
  monospace: 'monospace',
}

export const FONT_SCALES: Record<FontSizeKey, number> = {
  small: 0.875,
  normal: 1,
  large: 1.125,
}

export type Platform = 'mac' | 'windows' | 'linux'

/**
 * Detect the host OS so the font picker can offer options the user will
 * actually see. Prefers navigator.userAgentData (modern Chromium, Edge,
 * current Wails WebView) and falls back to the UA string — older Safari
 * and some Firefox builds still don't expose userAgentData.
 *
 * Unknown platforms fall back to Mac, which is the most common development
 * environment for this project.
 */
export function detectPlatform(): Platform {
  if (typeof navigator === 'undefined') return 'mac'

  // Modern: client hints (Chromium ≥ 90, Edge, Wails WebView on Win/Mac).
  const uaData = (navigator as unknown as {
    userAgentData?: { platform?: string }
  }).userAgentData
  const hintPlatform = uaData?.platform
  if (typeof hintPlatform === 'string' && hintPlatform.length > 0) {
    const p = hintPlatform.toLowerCase()
    if (p.includes('mac')) return 'mac'
    if (p.includes('win')) return 'windows'
    if (p.includes('linux') || p.includes('android') || p.includes('chrome os')) return 'linux'
  }

  // Fallback: parse the UA string. Matches Safari, older Firefox, WebView.
  const ua = navigator.userAgent.toLowerCase()
  if (ua.includes('mac os x') || ua.includes('macintosh') || ua.includes('iphone') || ua.includes('ipad')) {
    return 'mac'
  }
  if (ua.includes('windows')) return 'windows'
  if (ua.includes('linux') || ua.includes('android') || ua.includes('cros')) return 'linux'

  return 'mac'
}

const SANS_KEYS_BY_PLATFORM: Record<Platform, FontKey[]> = {
  mac: ['system', 'pingfang', 'georgia', 'sans-serif'],
  windows: ['system', 'yahei', 'times', 'sans-serif'],
  linux: ['system', 'noto-cjk', 'dejavu-serif', 'sans-serif'],
}

const MONO_KEYS_BY_PLATFORM: Record<Platform, MonoFontKey[]> = {
  mac: ['system', 'menlo', 'monaco', 'monospace'],
  windows: ['system', 'consolas', 'cascadia', 'monospace'],
  linux: ['system', 'dejavu-mono', 'liberation-mono', 'monospace'],
}

/** Keys visible in the sans-serif picker on the current platform. */
export function visibleSansKeys(platform: Platform = detectPlatform()): FontKey[] {
  return SANS_KEYS_BY_PLATFORM[platform]
}

/** Keys visible in the monospace picker on the current platform. */
export function visibleMonoKeys(platform: Platform = detectPlatform()): MonoFontKey[] {
  return MONO_KEYS_BY_PLATFORM[platform]
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
    // Apply size via CSS zoom on <html> so the multiplier reaches every
    // element — including the ~1000+ hard-coded `font-size: NNpx` rules
    // scattered across the frontend. The previous approach set
    // `--app-font-scale` and relied on `calc(NNpx * var(--app-font-scale))`,
    // but calc() only runs where the variable is consumed (a few TDesign
    // tokens plus the body reset), so users saw only parts of the UI
    // resize. Zoom composites the whole document at the requested factor
    // and is supported in all Chromium, WebKit, and modern Firefox (126+).
    //   https://developer.mozilla.org/en-US/docs/Web/CSS/zoom
    // setProperty is used instead of root.style.zoom because `zoom` is not
    // in the standard CSSStyleDeclaration type.
    root.style.setProperty('zoom', scale)
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
