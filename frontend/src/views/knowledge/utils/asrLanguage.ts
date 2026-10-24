/**
 * ASR 转写语言：下拉里给的是常用语种，但真正落库的必须是 ASR 厂商认的短代码
 * （whisper 之类要的是 zh / en / ja …，不是 Chinese / English）。
 *
 * 空字符串一直是“留空自动检测”的语义，所以下拉把空值呈现成一个显式的
 * “自动检测”选项（真实的选项值必须是非空字符串，否则 TDesign 会当成没选中），
 * 由 toAsrLanguageModel / fromAsrLanguageModel 在显示值与存储值之间转换。
 *
 * 下拉同时开了 creatable：es / fr / yue 这类不在预置列表里的代码直接手输也能用，
 * 存的就是用户输入的那个代码。
 */

/** 下拉里代表“留空自动检测”的选项值；真实的语言代码不会长成这样。 */
export const ASR_LANGUAGE_AUTO = '__auto__'

/** 预置语种，值必须和现有自由文本会传下去的值一致（厂商代码）。 */
export const ASR_LANGUAGE_VALUES = ['zh', 'en', 'ja', 'ko', 'ru'] as const

export type AsrLanguageValue = (typeof ASR_LANGUAGE_VALUES)[number]

/** 存储值 → 下拉的 modelValue。空值展示成“自动检测”，其余原样显示。 */
export function toAsrLanguageModel(stored: unknown): string {
  const code = normalizeAsrLanguage(stored)
  return code || ASR_LANGUAGE_AUTO
}

/**
 * 下拉的 modelValue → 存储值。清空（undefined）和“自动检测”都收敛成 ''
 * —— 和改造前留空即自动检测的语义一致，payload 里也始终是字符串。
 */
export function fromAsrLanguageModel(selected: unknown): string {
  const code = normalizeAsrLanguage(selected)
  return code === ASR_LANGUAGE_AUTO ? '' : code
}

/** 各种来源（DB、用户手输、清空操作）的输入收敛成一个去掉首尾空白的代码。 */
export function normalizeAsrLanguage(value: unknown): string {
  if (typeof value !== 'string') return ''
  return value.trim()
}
