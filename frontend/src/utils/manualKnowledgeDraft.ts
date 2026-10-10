import { stripCitationTagsForMarkdown } from './citationMarkdown'

/** Longest title we derive from a question before cutting it down. */
const MANUAL_TITLE_MAX = 32

/** A boundary cut has to leave at least this much of the question behind. */
const MANUAL_TITLE_MIN = 12

/** Punctuation that reads as noise at the end of a document title. */
const TRAILING_PUNCTUATION_RE = /[?？!！。.,，、;；:：\s]+$/

/** Clause boundaries we prefer to cut a long question on. */
const TITLE_BOUNDARIES = ['，', '。', '；', '：', '？', '！', '、', ',', ';', ':', ' ']

/**
 * Turn the asked question into a document title.
 *
 * A question mark and a hard cut mid-word ("……的多样性和创新...") both read as
 * an unfinished sentence in a knowledge list, so we drop the trailing
 * punctuation and, when the question is too long, cut it on the last clause
 * boundary instead of appending an ellipsis.
 */
export function deriveManualTitle(question: string, fallback: string): string {
  const condensed = (question || '').replace(/\s+/g, ' ').trim()
  if (!condensed) return fallback

  const stripped = condensed.replace(TRAILING_PUNCTUATION_RE, '').trim() || condensed
  if (stripped.length <= MANUAL_TITLE_MAX) return stripped

  const head = stripped.slice(0, MANUAL_TITLE_MAX)
  const boundary = TITLE_BOUNDARIES.reduce((best, char) => Math.max(best, head.lastIndexOf(char)), -1)
  // Only honour a boundary that still leaves a meaningful title behind.
  const cut = boundary >= MANUAL_TITLE_MIN ? head.slice(0, boundary) : head
  return cut.replace(TRAILING_PUNCTUATION_RE, '').trim() || head.trim()
}

/** A retrieved chunk reference, as persisted on an assistant message. */
export interface ManualDraftReference {
  knowledge_base_id?: string
}

/**
 * Collect knowledge-base ids cited by a chat answer, in reference order.
 *
 * Used as a soft preference when opening the manual editor from chat: the
 * editor resolves the first id that appears in its writable candidate list,
 * and otherwise keeps the existing first-option fallback. Viewer-only shares,
 * FAQ bases, and unknown ids are never force-inserted — those would reject
 * a save or create an invisible FAQ document.
 */
export function collectManualDraftPreferredKbIds(
  references?: ManualDraftReference[] | null,
): string[] {
  if (!Array.isArray(references)) return []
  const ids: string[] = []
  const seen = new Set<string>()
  for (const reference of references) {
    const kbId = reference?.knowledge_base_id
    if (typeof kbId !== 'string') continue
    const trimmed = kbId.trim()
    if (!trimmed || seen.has(trimmed)) continue
    seen.add(trimmed)
    ids.push(trimmed)
  }
  return ids
}

/**
 * Pick a preferred knowledge base that is already in the editor's candidates.
 *
 * Returns the first preferred id that appears in `candidateIds`, or the first
 * candidate (or '') when nothing matches — matching the editor's historical
 * `list[0]?.value ?? ''` fallback.
 */
export function resolvePreferredKnowledgeBaseId(
  preferredIds: string[] | null | undefined,
  candidateIds: string[],
): string {
  if (candidateIds.length === 0) return ''
  const candidates = new Set(candidateIds)
  for (const id of preferredIds || []) {
    if (candidates.has(id)) return id
  }
  return candidateIds[0] ?? ''
}

/**
 * @deprecated Prefer {@link collectManualDraftPreferredKbIds} + editor-side
 * resolution against the writable candidate list. Kept as a thin first-id
 * helper for call sites that only need the raw citation order.
 */
export function resolveManualDraftKnowledgeBaseId(
  references?: ManualDraftReference[] | null,
): string | null {
  const ids = collectManualDraftPreferredKbIds(references)
  return ids[0] ?? null
}

/**
 * Build the Markdown body seeded into the manual editor from a chat answer.
 *
 * The raw answer carries inline `<kb/>` citation tags whose chunk/kb UUIDs are
 * meaningless once the text leaves the conversation — they used to land in the
 * editor verbatim and had to be deleted by hand. Strip them, keep `<web/>`
 * citations as real links, and list the cited documents once at the bottom so
 * the provenance survives.
 */
export function buildManualDraft(
  answer: string,
  labels: { emptyAnswer: string; sourcesHeading: string },
): string {
  const { text, sources } = stripCitationTagsForMarkdown(answer || '')
  const body = text.trim() || labels.emptyAnswer
  if (!sources.length) return body

  const list = sources.map((source) => `- ${source}`).join('\n')
  return `${body}\n\n---\n\n**${labels.sourcesHeading}**\n\n${list}\n`
}
