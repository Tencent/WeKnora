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
      i18n.global.setLocaleMessage(target, mod.default as never)
      loadedLocales.add(target)
      return target
    })
    .finally(() => {
      inflightLoads.delete(target)
    })
  inflightLoads.set(target, task)
  return task
}

export async function setI18nLanguage(rawLocale: string): Promise<SupportedMainLocale> {
  const target = await loadLocaleMessages(rawLocale)
  i18n.global.locale.value = target
  writeStoredLocale(target)
  return target
}

let bootstrapPromise: Promise<SupportedMainLocale> | null = null

export function initI18n(): Promise<SupportedMainLocale> {
  if (!bootstrapPromise) {
    const active = normalizeMainLocale(i18n.global.locale.value || savedLocale)
    bootstrapPromise = loadLocaleMessages(active).then((loaded) => {
      i18n.global.locale.value = loaded
      return loaded
    })
  }
  return bootstrapPromise
}

watch(
  () => i18n.global.locale.value,
  (next) => {
    if (next && !isEmbedSurface()) {
      void loadLocaleMessages(next)
    }
  },
)

if (typeof window !== 'undefined' && !isEmbedSurface()) {
  void initI18n()
}

export default i18n
