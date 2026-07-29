import type { WikiBlockSource, WikiPageBlock } from '../api/wiki/index.ts'

export interface NumberedWikiBlockSource extends WikiBlockSource {
  citationNumber: number
  citationKey: string
}

export interface WikiBlockCitationView {
  block: WikiPageBlock
  citationNumbers: number[]
}

export interface WikiCitationModel {
  blocks: WikiBlockCitationView[]
  sources: NumberedWikiBlockSource[]
}

export interface WikiProvenanceCitationGroup {
  key: string
  position: number
  citationNumbers: number[]
  provenanceStatus: string
  authorType: string
  structural: boolean
}

export interface WikiProvenanceRenderEntry {
  key: string
  blockType: string
  content: string
  grouped: boolean
  groupKey: string
  citationGroups: WikiProvenanceCitationGroup[]
}

// Prefer the server-provided key so citation identity can evolve without a UI
// migration. The fallback keeps the first provenance API revision compatible.
export function wikiSourceCitationKey(source: WikiBlockSource): string {
  const explicit = source.citation_key?.trim()
  if (explicit) return explicit

  return [
    source.knowledge_id,
    source.knowledge_attempt ?? '',
    source.chunk_id,
    source.chunk_revision ?? '',
    source.evidence_hash || source.evidence,
  ].join('\u0000')
}

// Number sources in first-appearance order across the page. Reusing the same
// evidence on several paragraphs therefore keeps one stable footnote number.
export function buildWikiCitationModel(blocks: WikiPageBlock[] | null | undefined): WikiCitationModel {
  if (!blocks?.length) return { blocks: [], sources: [] }

  const orderedBlocks = blocks
    .map((block, index) => ({ block, index }))
    .sort((a, b) => a.block.sort_order - b.block.sort_order || a.index - b.index)

  const numberByKey = new Map<string, number>()
  const sources: NumberedWikiBlockSource[] = []
  const blockViews: WikiBlockCitationView[] = []

  for (const { block } of orderedBlocks) {
    const citationNumbers: number[] = []
    const seenInBlock = new Set<number>()

    for (const source of block.sources || []) {
      const citationKey = wikiSourceCitationKey(source)
      let citationNumber = numberByKey.get(citationKey)
      if (citationNumber === undefined) {
        citationNumber = sources.length + 1
        numberByKey.set(citationKey, citationNumber)
        sources.push({ ...source, citationNumber, citationKey })
      }
      if (!seenInBlock.has(citationNumber)) {
        seenInBlock.add(citationNumber)
        citationNumbers.push(citationNumber)
      }
    }

    blockViews.push({ block, citationNumbers })
  }

  return { blocks: blockViews, sources }
}

export function isWikiTableDelimiter(content: string): boolean {
  const cells = content.trim().replace(/^\|/, '').replace(/\|$/, '').split('|')
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell.trim()))
}

function appendMarkdownFragment(current: string, next: string): string {
  if (!current || !next || /(?:\r?\n)$/.test(current) || /^(?:\r?\n)/.test(next)) {
    return current + next
  }
  return `${current}\n${next}`
}

// Adjacent table rows and list items must be parsed as one Markdown fragment.
// Each original block still gets a separate citation group so a combined table
// or list never blurs which row/item owns which evidence and coverage status.
export function buildWikiProvenanceRenderEntries(
  blocks: WikiBlockCitationView[] | null | undefined,
): WikiProvenanceRenderEntry[] {
  if (!blocks?.length) return []

  const entries: WikiProvenanceRenderEntry[] = []

  for (const entry of blocks) {
    const blockType = entry.block.block_type || 'paragraph'
    const groupKey = blockType === 'list_item'
      ? `${blockType}:${/^\s*\d+[.)]\s/.test(entry.block.content) ? 'ordered' : 'unordered'}`
      : blockType
    // Published pipeline blocks without evidence can only be syntax/layout:
    // the backend rejects every sourceable pipeline claim that is not fully
    // verified. User/agent blocks stay visible and receive an authorship tag.
    const pipelineLayoutOnly = (entry.block.author_type || 'pipeline') === 'pipeline'
      && entry.block.provenance_status === 'unsupported'
      && !entry.citationNumbers.length
    const structural = blockType === 'heading'
      || (blockType === 'table_row' && isWikiTableDelimiter(entry.block.content))
      || pipelineLayoutOnly
    const groupable = blockType === 'table_row' || blockType === 'list_item'
    const previous = entries[entries.length - 1]

    if (groupable && previous?.groupKey === groupKey) {
      previous.content = appendMarkdownFragment(previous.content, entry.block.content)
      previous.grouped = true
      const position = structural
        ? 0
        : previous.citationGroups.filter((group) => !group.structural).length + 1
      previous.citationGroups.push({
        key: entry.block.id,
        position,
        citationNumbers: [...entry.citationNumbers],
        provenanceStatus: entry.block.provenance_status || '',
        authorType: entry.block.author_type || 'pipeline',
        structural,
      })
      continue
    }

    entries.push({
      key: entry.block.id,
      blockType,
      groupKey,
      content: entry.block.content,
      grouped: false,
      citationGroups: [{
        key: entry.block.id,
        position: structural ? 0 : 1,
        citationNumbers: [...entry.citationNumbers],
        provenanceStatus: entry.block.provenance_status || '',
        authorType: entry.block.author_type || 'pipeline',
        structural,
      }],
    })
  }

  return entries
}
