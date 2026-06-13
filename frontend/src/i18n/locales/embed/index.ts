/**
 * Embed i18n entry point.
 *
 * - Visitor UI strings: `frontend/src/i18n/embed.ts` (loaded by embed-main.ts)
 * - Admin `embedPublish` strings: `frontend/src/i18n/locales/*.ts`
 *
 * Re-export locale helpers so feature code can import from one path.
 */
export {
  applyEmbedLocale,
  EMBED_LOCALE_STORAGE_KEY,
  normalizeEmbedLocale,
  readEmbedLocaleFromUrl,
  syncEmbedLocaleFromUrl,
  type EmbedLocale,
} from '@/i18n/embed'
