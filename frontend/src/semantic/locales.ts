// 数据建模模块自带的 i18n 消息。启动时通过 mergeLocaleMessage 合并进主
// i18n 实例 (见 index.ts), 上游 4 个 locale 文件保持零改动。
// ru-RU / ko-KR 暂以英文兜底, 后续补翻。
import i18n from '@/i18n'
import { zhCN, enUS } from './messages'

const MESSAGES: Record<string, Record<string, unknown>> = {
  'zh-CN': { semantic: zhCN, menu: { semantic: zhCN.menu } },
  'en-US': { semantic: enUS, menu: { semantic: enUS.menu } },
  'ru-RU': { semantic: enUS, menu: { semantic: enUS.menu } },
  'ko-KR': { semantic: enUS, menu: { semantic: enUS.menu } }
}

export function mergeLocaleMessage() {
  for (const [locale, messages] of Object.entries(MESSAGES)) {
    i18n.global.mergeLocaleMessage(locale, messages)
  }
}
