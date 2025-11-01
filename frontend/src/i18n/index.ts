import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.ts'
import ruRU from './locales/ru-RU.ts'
import enUS from './locales/en-US.ts'

const messages = {
  'zh-CN': zhCN,
  'ru-RU': ruRU,
  'en-US': enUS
}

// Получаем сохраненный язык из localStorage или используем китайский по умолчанию
const savedLocale = localStorage.getItem('locale') || 'zh-CN'
console.log('i18n инициализация с языком:', savedLocale)

const i18n = createI18n({
  legacy: false,
  locale: savedLocale,
  fallbackLocale: 'zh-CN',
  globalInjection: true,
  messages
})

export default i18n