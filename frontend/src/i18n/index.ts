import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.ts'
import ruRU from './locales/ru-RU.ts'
import enUS from './locales/en-US.ts'
import koKR from './locales/ko-KR.ts'

const messages = {
  'zh-CN': zhCN,
  'en-US': enUS,
  'ru-RU': ruRU,
  'ko-KR': koKR
}

const SUPPORTED_LOCALES = Object.keys(messages)
const BUILT_IN_DEFAULT = 'zh-CN'

// Resolve the deployment default locale. Mirrors the MAX_FILE_SIZE_MB convention:
// runtime config (.env -> docker-entrypoint -> window.__RUNTIME_CONFIG__) wins over
// build-time env (VITE_DEFAULT_LOCALE), then the built-in default. Unknown values
// (e.g. a typo in .env) fall back to the built-in default instead of a blank UI.
function resolveDefaultLocale(): string {
  const candidate = window.__RUNTIME_CONFIG__?.DEFAULT_LOCALE
    || import.meta.env.VITE_DEFAULT_LOCALE
    || BUILT_IN_DEFAULT
  return SUPPORTED_LOCALES.includes(candidate) ? candidate : BUILT_IN_DEFAULT
}

// User's explicit past choice wins; otherwise use the deployment default.
const savedLocale = localStorage.getItem('locale') || resolveDefaultLocale()

const i18n = createI18n({
  legacy: false,
  locale: savedLocale,
  fallbackLocale: 'zh-CN',
  globalInjection: true,
  // Some translations intentionally embed `<strong>` markup (e.g. agent step summaries).
  // We render them via v-html with our own sanitization, so silence vue-i18n's HTML warning
  // to avoid flooding the console and slowing renders during history loads.
  warnHtmlMessage: false,
  messages
})

export default i18n