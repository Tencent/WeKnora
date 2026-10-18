/**
 * Video narration languages.
 *
 * The values are the short codes ASR vendors (Whisper, Qwen ASR, …) accept;
 * the option labels come from i18n so they are not hard-coded in one language.
 * An empty string means "auto-detect", which is also what the select stores
 * when the user clears it — never a sentinel such as `__auto__`, because the
 * value is sent to the backend as `video_config.language` verbatim.
 */
export const VIDEO_LANGUAGE_VALUES = ['zh', 'en', 'ja', 'ko', 'ru'] as const

export type VideoLanguagePreset = (typeof VIDEO_LANGUAGE_VALUES)[number]

/**
 * Normalise whatever the select emits into the string stored on the payload.
 *
 * `creatable` keeps es / fr / yue and friends typeable, so the model is a plain
 * string rather than the preset union. Clearing the select gives `undefined` or
 * `null`; non-string values collapse to '' (auto-detect) instead of leaking a
 * number or object into `video_config.language`.
 */
export function normalizeVideoLanguage(value: unknown): string {
  if (typeof value !== 'string') return ''
  return value.trim()
}
