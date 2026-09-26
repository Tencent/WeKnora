/** Text with locale variants, as plugin manifests serve it: { default, 'zh-CN': ... }. */
export type LocalizedText = { default: string } & Record<string, string>

/** Resolves localized text for a locale: exact, then same language, then default. */
export function localizedText(text: LocalizedText | undefined, locale: string): string {
  if (!text) return ''
  if (text[locale]) return text[locale]
  const lang = locale.split('-')[0]
  const match = Object.keys(text).sort().find((k) => k !== 'default' && k.split('-')[0] === lang && text[k])
  return match ? text[match] : text.default
}

/**
 * Picks a variant from a locale map without a default ({ 'zh-CN': ... }, as
 * plugin connectors and provider types send names): exact, then same
 * language, else undefined.
 */
export function pickLocale(variants: Record<string, string> | undefined, locale: string): string | undefined {
  if (!variants) return undefined
  if (variants[locale]) return variants[locale]
  const lang = locale.split('-')[0].toLowerCase()
  const key = Object.keys(variants)
    .sort()
    .find((k) => k.split('-')[0].toLowerCase() === lang)
  return key ? variants[key] : undefined
}
