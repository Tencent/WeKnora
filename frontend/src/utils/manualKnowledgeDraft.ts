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
 * Pick the knowledge base a chat answer belongs to, for the manual editor.
 *
 * The "add answer to knowledge base" action in chat opens the editor without
 * naming a knowledge base. The editor then defaults the field to `list[0]` —
 * the first knowledge base the user happens to own, which is usually unrelated
 * to the conversation. A user with no writable knowledge base has nothing to
 * fall back to and cannot publish at all. Either way the answer lands somewhere
 * the user did not intend, and there is no cue in the UI that a choice was made
 * for them (#2081).
 *
 * An answer that cites sources was retrieved from a specific knowledge base, so
 * preselect that and let the user override it. Returns null when the answer has
 * no retrievable provenance — a plain chat turn, or a reply whose references
 * never arrived — which leaves the existing first-option fallback untouched.
 *
 * The first reference wins so the choice is stable across renders; a mixed-KB
 * answer lands in the base of its first citation.
 */
export function resolveManualDraftKnowledgeBaseId(
  references?: ManualDraftReference[] | null,
): string | null {
  if (!Array.isArray(references)) return null
  for (const reference of references) {
    const kbId = reference?.knowledge_base_id
    if (typeof kbId === 'string' && kbId.trim()) return kbId.trim()
  }
  return null
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
