import { findTextMatches, normalizeForMatch, type TextMatch } from './sourceLocator'

/** Exact fragments for parsers that serialize table columns in a different order.
 * Every returned range is independently checked. Callers must report partial
 * coverage; unmatched text is never included in a highlight.
 */
export function findSourceFragments(pages: string[], scope: string): Array<TextMatch & { page: number; quote: string }> {
  const table = scope.includes('|')
  const parts = table ? scope.split(/[|\n]/u) : scope.split(/(?<=[。！？；!?;])|\n+|(?<=[\p{Script=Han}】）])\s*[à→\uE000-\uF8FF]\s*/u)
  const candidates = new Set<string>()
  for (const part of parts) {
    const clean = part.trim()
    if (!clean || /^[-: ]+$/.test(clean)) continue
    if (!table) { candidates.add(clean); continue }
    // Preserve whole cells first. Flattened cells may hold a list of courses;
    // use complete whitespace-delimited entries, never arbitrary n-grams.
    candidates.add(clean)
    const words = clean.split(/\s+/u)
    if (words.length > 1) for (const word of words) candidates.add(word)
  }
  const evidence = [...candidates].filter(q => normalizeForMatch(q).length >= (table ? 5 : 8))
    .map(quote => ({ quote, hits: pages.flatMap((text, i) => findTextMatches(text, quote).map(hit => ({ ...hit, page: i + 1 }))) }))
  const unique = evidence.filter(e => e.hits.length === 1)
  // Require independent anchors. A shared opening alone is not a location.
  const anchors = unique.filter(e => !unique.some(other => other !== e && normalizeForMatch(other.quote).includes(normalizeForMatch(e.quote))))
  let anchorPages = new Set(anchors.map(e => e.hits[0]!.page))
  if (anchors.length < 2) {
    // Repeated table entries can still identify a page by their intersection:
    // all matching cells must agree, not merely the highest-scoring page.
    const present = evidence.filter(e => e.hits.length)
    if (!table || present.length < 2) return []
    let common = new Set(present[0]!.hits.map(hit => hit.page))
    for (const entry of present.slice(1)) common = new Set([...common].filter(page => entry.hits.some(hit => hit.page === page)))
    if (common.size !== 1) return []
    anchorPages = common
  }
  const ranges: Array<TextMatch & { page: number; quote: string }> = []
  for (const { quote, hits } of evidence) {
    const scoped = hits.length === 1 ? hits : hits.filter(hit => anchorPages.has(hit.page))
    if (scoped.length !== 1) continue
    const hit = scoped[0]!
    ranges.push({ ...hit, quote: pages[hit.page - 1]!.slice(hit.start, hit.end) })
  }
  const nonOverlapping = ranges.filter((hit, i) => !ranges.some((other, j) => j !== i && other.page === hit.page &&
    other.start <= hit.start && other.end >= hit.end && (other.start < hit.start || other.end > hit.end || j < i)))
  if (!table) {
    // Images and OCR artifacts can occupy most of a chunk. Coverage percentage
    // is not evidence of location: require multiple complete, unique sentences
    // in the same local source order, and report the result as partial.
    for (let i = 1; i < nonOverlapping.length; i++) {
      const previous = nonOverlapping[i - 1]!, current = nonOverlapping[i]!
      if (current.page < previous.page || (current.page === previous.page && current.start < previous.end)) return []
    }
    if (nonOverlapping.length < 2 || nonOverlapping.at(-1)!.page - nonOverlapping[0]!.page > 2) return []
  }
  return nonOverlapping.sort((a, b) => a.page - b.page || a.start - b.start)
}

/** Rank already verified ranges by verbatim overlap with the cited sentence.
 * This changes navigation order only; it cannot create a match or resolve ambiguity.
 */
export function sourceOverlapLength(quote: string, sentence: string): number {
  const source = normalizeForMatch(quote), answer = normalizeForMatch(sentence)
  let previous = new Uint32Array(answer.length + 1), best = 0
  for (const character of source) {
    const current = new Uint32Array(answer.length + 1)
    for (let j = 0; j < answer.length; j++) if (character === answer[j]) {
      current[j + 1] = previous[j]! + 1
      best = Math.max(best, current[j + 1]!)
    }
    previous = current
  }
  return best
}
