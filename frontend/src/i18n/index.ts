import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.ts'
import zhTW from './locales/zh-TW.ts'
import ruRU from './locales/ru-RU.ts'
import enUS from './locales/en-US.ts'
import koKR from './locales/ko-KR.ts'
import { BUILT_IN_DEFAULT, resolveDefaultLocale } from './resolveDefaultLocale.ts'

const messages = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  'en-US': enUS,
  'ru-RU': ruRU,
  'ko-KR': koKR
}

// User's explicit past choice wins; otherwise use the deployment default.
const savedLocale = localStorage.getItem('locale') || resolveDefaultLocale(
  window.__RUNTIME_CONFIG__?.DEFAULT_LOCALE,
  import.meta.env.VITE_DEFAULT_LOCALE,
)

const i18n = createI18n({
  legacy: false,
  locale: savedLocale,
  fallbackLocale: BUILT_IN_DEFAULT,
  globalInjection: true,
  // Some translations intentionally embed `<strong>` markup (e.g. agent step summaries).
  // We render them via v-html with our own sanitization, so silence vue-i18n's HTML warning
  // to avoid flooding the console and slowing renders during history loads.
  warnHtmlMessage: false,
  messages
})

export default i18n
