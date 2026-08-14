export const SUPPORTED_LOCALES = ['zh-CN', 'en-US', 'ru-RU', 'ko-KR'] as const
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number]

export const BUILT_IN_DEFAULT: SupportedLocale = 'zh-CN'

function isSupportedLocale(value: string): value is SupportedLocale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(value)
}

/** Resolve deployment default locale before any user preference is applied. */
export function resolveDefaultLocale(
  runtimeLocale?: string | null,
  buildTimeLocale?: string | null,
): SupportedLocale {
  const raw = runtimeLocale || buildTimeLocale || BUILT_IN_DEFAULT
  const candidate = typeof raw === 'string' ? raw.trim() : BUILT_IN_DEFAULT
  return isSupportedLocale(candidate) ? candidate : BUILT_IN_DEFAULT
}
