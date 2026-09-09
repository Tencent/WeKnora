import type { GlobalConfigProvider } from 'tdesign-vue-next'
import enUS from 'tdesign-vue-next/esm/locale/en_US'
import zhCN from 'tdesign-vue-next/esm/locale/zh_CN'
import koKR from 'tdesign-vue-next/esm/locale/ko_KR'
import jaJP from 'tdesign-vue-next/esm/locale/ja_JP'
import ruRU from 'tdesign-vue-next/esm/locale/ru_RU'
import frFR from './fr-FR.ts'
import type { SupportedLocale } from '../resolveDefaultLocale.ts'

const locales = {
  'en-US': enUS,
  'zh-CN': zhCN,
  'ko-KR': koKR,
  'ja-JP': jaJP,
  'ru-RU': ruRU,
  'fr-FR': frFR,
} satisfies Record<SupportedLocale, unknown>

/** Shared by the main application and the independent embed application. */
export function getTDesignLocale(locale: string): GlobalConfigProvider {
  // TDesign ships readonly locale arrays but ConfigProvider declares mutable
  // ones. The provider only reads the dictionary.
  return (locales[locale as SupportedLocale] || enUS) as GlobalConfigProvider
}
