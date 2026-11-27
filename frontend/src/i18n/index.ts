import { watch } from 'vue'
import { createI18n } from 'vue-i18n'
import { isEmbedPage } from '../utils/authRefresh.ts'
import { pluralRules } from './pluralizationRules.ts'
import { BUILT_IN_DEFAULT, resolveDefaultLocale } from './resolveDefaultLocale.ts'

export const SUPPORTED_MAIN_LOCALES = [
  'zh-CN',
  'zh-TW',
  'en-US',
  'ru-RU',
  'ko-KR',
  'ja-JP',
  'vi-VN',
] as const

export type SupportedMainLocale = (typeof SUPPORTED_MAIN_LOCALES)[number]

type LocaleModule = { default: Record<string, unknown> }

const LOCALE_LOADERS: Record<SupportedMainLocale, () => Promise<LocaleModule>> = {
  'zh-CN': () => import('./locales/zh-CN.ts'),
  'zh-TW': () => import('./locales/zh-TW.ts'),
  'en-US': () => import('./locales/en-US.ts'),
  'ru-RU': () => import('./locales/ru-RU.ts'),
  'ko-KR': () => import('./locales/ko-KR.ts'),
  'ja-JP': () => import('./locales/ja-JP.ts'),
  'vi-VN': () => import('./locales/vi-VN.ts'),
}

export function normalizeMainLocale(raw?: string | null): SupportedMainLocale {
  const trimmed = raw?.trim() ?? ''
  if ((SUPPORTED_MAIN_LOCALES as readonly string[]).includes(trimmed)) {
    return trimmed as SupportedMainLocale
  }
  const lower = trimmed.toLowerCase()
  if (
    lower === 'zh-tw' ||
    lower === 'zh-hk' ||
    lower === 'zh-mo' ||
    lower.startsWith('zh-hant')
  ) {
    return 'zh-TW'
  }
  if (lower.startsWith('zh')) return 'zh-CN'
  if (lower.startsWith('en')) return 'en-US'
  if (lower.startsWith('ru')) return 'ru-RU'
  if (lower.startsWith('ko')) return 'ko-KR'
  if (lower.startsWith('ja')) return 'ja-JP'
  if (lower.startsWith('vi')) return 'vi-VN'
  return BUILT_IN_DEFAULT as SupportedMainLocale
}

function readStoredLocale(): string | null {
  try {
    if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
      return localStorage.getItem('locale')
    }
  } catch {
    // localStorage may be unavailable in restricted/private contexts or Node tests.
  }
  return null
}

function writeStoredLocale(locale: string): void {
  try {
    if (typeof localStorage !== 'undefined' && typeof localStorage.setItem === 'function') {
      localStorage.setItem('locale', locale)
    }
  } catch {
    // Ignore storage quota/security errors.
  }
}

function resolveInitialLocale(): SupportedMainLocale {
  const runtimeDefault =
    typeof window !== 'undefined' ? window.__RUNTIME_CONFIG__?.DEFAULT_LOCALE : undefined
  const viteEnv = (import.meta as { env?: Record<string, string | undefined> }).env
  const initial =
    readStoredLocale() || resolveDefaultLocale(runtimeDefault, viteEnv?.VITE_DEFAULT_LOCALE)
  return normalizeMainLocale(initial)
}

function isEmbedSurface(): boolean {
  if (isEmbedPage()) return true
  if (typeof document !== 'undefined' && typeof document.getElementById === 'function') {
    return document.getElementById('embed-app') !== null
  }
  return false
}

const savedLocale = resolveInitialLocale()

const i18n = createI18n({
  legacy: false,
  locale: savedLocale,
  fallbackLocale: BUILT_IN_DEFAULT,
  globalInjection: true,
  pluralRules,
  // Some translations intentionally embed `<strong>` markup (e.g. agent step summaries).
  // We render them via v-html with our own sanitization, so silence vue-i18n's HTML warning
  // to avoid flooding the console and slowing renders during history loads.
  warnHtmlMessage: false,
  messages: {},
})

const loadedLocales = new Set<SupportedMainLocale>()
const inflightLoads = new Map<SupportedMainLocale, Promise<SupportedMainLocale>>()
const embedSeedMessages = new Map<SupportedMainLocale, Record<string, unknown>>()

function mergePlainRecords(
  base: Record<string, unknown>,
  override: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...base }
  for (const [key, val] of Object.entries(override)) {
    const prev = out[key]
    if (
      val &&
      typeof val === 'object' &&
      !Array.isArray(val) &&
      prev &&
      typeof prev === 'object' &&
      !Array.isArray(prev)
    ) {
      out[key] = mergePlainRecords(
        prev as Record<string, unknown>,
        val as Record<string, unknown>,
      )
    } else {
      out[key] = val
    }
  }
  return out
}

export function isLocaleLoaded(locale: string): boolean {
  return loadedLocales.has(normalizeMainLocale(locale))
}

export async function loadLocaleMessages(rawLocale: string): Promise<SupportedMainLocale> {
  const target = normalizeMainLocale(rawLocale)
  if (loadedLocales.has(target)) {
    return target
  }
  const existing = inflightLoads.get(target)
  if (existing) {
    return existing
  }
  const task = LOCALE_LOADERS[target]()
    .then((mod) => {
      const embedSeed = embedSeedMessages.get(target)
      const bundle = embedSeed ? mergePlainRecords(embedSeed, mod.default) : mod.default
      i18n.global.setLocaleMessage(target, bundle as never)
      loadedLocales.add(target)
      return target
    })
    .finally(() => {
      inflightLoads.delete(target)
    })
  inflightLoads.set(target, task)
  return task
}

export function syncEmbedSharedLocale(rawLocale: string): SupportedMainLocale {
  const target = normalizeMainLocale(rawLocale)
  i18n.global.locale.value = target
  void loadLocaleMessages(target).catch(() => {
    // Non-fatal on embed: seeded EMBED_MESSAGES remain available offline.
  })
  return target
}

export function seedEmbedLocaleMessages(
  bundles: Partial<Record<SupportedMainLocale, Record<string, unknown>>>,
  activeLocale: string,
): SupportedMainLocale {
  for (const locale of SUPPORTED_MAIN_LOCALES) {
    const seed = bundles[locale]
    if (!seed) continue
    embedSeedMessages.set(locale, seed)
    if (loadedLocales.has(locale)) {
      const current = i18n.global.getLocaleMessage(locale) as unknown as Record<string, unknown>
      i18n.global.setLocaleMessage(locale, mergePlainRecords(seed, current) as never)
    } else {
      i18n.global.setLocaleMessage(locale, seed as never)
    }
  }
  return syncEmbedSharedLocale(activeLocale)
}

let languageSwitchSeq = 0

export async function setI18nLanguage(rawLocale: string): Promise<SupportedMainLocale> {
  const seq = ++languageSwitchSeq
  const target = await loadLocaleMessages(rawLocale)
  if (seq !== languageSwitchSeq) {
    return normalizeMainLocale(i18n.global.locale.value)
  }
  i18n.global.locale.value = target
  writeStoredLocale(target)
  return target
}

let bootstrapPromise: Promise<SupportedMainLocale> | null = null

export function initI18n(): Promise<SupportedMainLocale> {
  if (!bootstrapPromise) {
    const active = normalizeMainLocale(i18n.global.locale.value || savedLocale)
    const task = loadLocaleMessages(active)
      .then((loaded) => {
        i18n.global.locale.value = loaded
        return loaded
      })
      .catch((err) => {
        bootstrapPromise = null
        throw err
      })
    bootstrapPromise = task
  }
  return bootstrapPromise
}

watch(
  () => i18n.global.locale.value,
  (next) => {
    if (next && !isEmbedSurface()) {
      void loadLocaleMessages(next).catch(() => {})
    }
  },
)

if (typeof window !== 'undefined' && !isEmbedSurface()) {
  void initI18n().catch((err) => {
    console.warn('[WeKnora] Initial locale bundle failed to preload; will retry on bootstrap:', err)
  })
}

export default i18n
