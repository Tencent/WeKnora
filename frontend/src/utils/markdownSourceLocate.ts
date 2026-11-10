import { renderDocumentPreviewMarkdown } from './documentPreviewMarkdown'
import { buildTextIndex, findTextRange } from './sourceLocatorDom'
import { findInText, normalizeForMatch, quotedSourceExcerpt } from './sourceLocator'
import { sourceOverlapLength } from './fragmentedSource'

/** Match the same rendered text on both sides, including fenced/inline code.
 * Markdown syntax and fence languages are not visible source text. Rendering
 * into an inert template also preserves code containing HTML-like text.
 */
export function findMarkdownSourceRange(root: Element, markdown: string, sentence = ''): { range: Range; ranges?: Range[]; narrowed: boolean; exact: boolean } | null {
  const template = root.ownerDocument.createElement('template')
  template.innerHTML = renderDocumentPreviewMarkdown(markdown)
  const source = buildTextIndex(template.content).text
  // Establish the complete source region before narrowing. A similar paragraph
  // elsewhere in the document cannot rescue a missing or ambiguous source.
  const region = findTextRange(root, source)
  if (!region) return null
  const excerpt = quotedSourceExcerpt(source, sentence) || sentence
  if (excerpt && findInText(source, excerpt)) {
    const exact = findTextRange(root, excerpt)
    if (exact && region.isPointInRange(exact.startContainer, exact.startOffset) && region.isPointInRange(exact.endContainer, exact.endOffset)) {
      return { range: exact, narrowed: true, exact: true }
    }
  }
  // Recover paragraph boundaries from the original DOM, not the rendered
  // chunk. A child can end halfway through an original paragraph. Its complete
  // unique match above establishes the occurrence; only intersecting blocks
  // may provide surrounding context, never a similar neighbouring paragraph.
  const blocks = [...root.querySelectorAll('p, li, pre, td, th')]
    .filter(e => !e.querySelector('p, li, pre, td, th') && region.intersectsNode(e))
    .flatMap(element => {
      const quote = buildTextIndex(element).text.trim()
      const range = findTextRange(element, quote)
      if (!range) return []
      const overlap = range.cloneRange()
      if (overlap.compareBoundaryPoints(Range.START_TO_START, region) < 0) overlap.setStart(region.startContainer, region.startOffset)
      if (overlap.compareBoundaryPoints(Range.END_TO_END, region) > 0) overlap.setEnd(region.endContainer, region.endOffset)
      // A delimiter or a couple of boundary characters cannot attach an
      // otherwise unsupported paragraph to the cited source.
      if (normalizeForMatch(overlap.toString()).length < 8) return []
      return [{ quote, range }]
    })
  // Chinese clauses provide stronger anchors than repeated command names.
  // Accept complete source runs only, not fuzzy substrings of a guessed block.
  const clauses = blocks.filter(({ quote }) => [...quote.matchAll(/[\p{Script=Han}]{8,}/gu)]
    .some(m => findInText(sentence, m[0])))
  if (clauses.length && clauses.length <= 3) {
    const ranges = clauses.map(block => block.range)
    return { range: ranges[0]!, ranges, narrowed: true, exact: false }
  }
  // Verbatim overlap only ranks paragraphs tied to the verified source. The
  // paragraph context is not a claim of word-for-word quotation coverage.
  const ranked = blocks.map(block => ({ ...block, score: sourceOverlapLength(block.quote, sentence.slice(-300)) }))
    .sort((a, b) => b.score - a.score)
  const best = ranked[0]
  if (best && best.score >= 8 && best.score >= (ranked[1]?.score || 0) + 4) {
    return { range: best.range, narrowed: true, exact: false }
  }
  return { range: region, narrowed: false, exact: true }
}
