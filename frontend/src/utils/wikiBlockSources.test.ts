import assert from 'node:assert/strict'
import test from 'node:test'

import type { WikiBlockSource, WikiPageBlock } from '../api/wiki/index.ts'
import {
  buildWikiCitationModel,
  buildWikiProvenanceRenderEntries,
  wikiSourceCitationKey,
} from './wikiBlockSources.ts'

function source(overrides: Partial<WikiBlockSource> = {}): WikiBlockSource {
  return {
    id: 'source-1',
    knowledge_id: 'knowledge-1',
    knowledge_attempt: 1,
    chunk_id: 'chunk-1',
    chunk_revision: 1,
    evidence: 'supporting evidence',
    validation_status: 'verified',
    ...overrides,
  }
}

function block(
  id: string,
  sortOrder: number,
  sources: WikiBlockSource[],
  overrides: Partial<WikiPageBlock> = {},
): WikiPageBlock {
  return {
    id,
    block_type: 'paragraph',
    sort_order: sortOrder,
    content: id,
    sources,
    ...overrides,
  }
}

test('reuses one citation number for the same evidence across blocks', () => {
  const shared = source({ citation_key: 'stable-source' })
  const model = buildWikiCitationModel([
    block('b1', 1, [shared]),
    block('b2', 2, [{ ...shared, id: 'source-copy' }]),
  ])

  assert.equal(model.sources.length, 1)
  assert.deepEqual(model.blocks.map((entry) => entry.citationNumbers), [[1], [1]])
})

test('numbers distinct evidence in first block appearance order', () => {
  const second = source({ id: 's2', citation_key: 'second', chunk_id: 'chunk-2' })
  const first = source({ id: 's1', citation_key: 'first' })
  const model = buildWikiCitationModel([
    block('later-input', 20, [second]),
    block('first-by-sort', 10, [first, second]),
  ])

  assert.deepEqual(model.blocks.map((entry) => entry.block.id), ['first-by-sort', 'later-input'])
  assert.deepEqual(model.sources.map((entry) => entry.citationKey), ['first', 'second'])
  assert.deepEqual(model.blocks.map((entry) => entry.citationNumbers), [[1, 2], [2]])
})

test('deduplicates repeated evidence inside one block', () => {
  const repeated = source({ citation_key: 'same' })
  const model = buildWikiCitationModel([
    block('b1', 1, [repeated, { ...repeated, id: 'duplicate-row' }]),
  ])

  assert.deepEqual(model.blocks[0]?.citationNumbers, [1])
  assert.equal(model.sources.length, 1)
})

test('uses knowledge attempt, chunk revision, and evidence as the legacy key fallback', () => {
  const a = source({ evidence: 'claim A', evidence_hash: undefined })
  const b = source({ evidence: 'claim B', evidence_hash: undefined })
  assert.notEqual(wikiSourceCitationKey(a), wikiSourceCitationKey(b))

  const hashedA = source({ evidence: 'format A', evidence_hash: 'same-hash' })
  const hashedB = source({ evidence: 'format B', evidence_hash: 'same-hash' })
  assert.equal(wikiSourceCitationKey(hashedA), wikiSourceCitationKey(hashedB))

  const nextRevision = source({ evidence_hash: 'same-hash', chunk_revision: 2 })
  assert.notEqual(wikiSourceCitationKey(hashedA), wikiSourceCitationKey(nextRevision))
})

test('handles empty blocks and source-less blocks', () => {
  assert.deepEqual(buildWikiCitationModel(undefined), { blocks: [], sources: [] })

  const model = buildWikiCitationModel([block('manual', 1, [])])
  assert.equal(model.sources.length, 0)
  assert.deepEqual(model.blocks[0]?.citationNumbers, [])
})

test('groups adjacent table rows without losing row provenance', () => {
  const model = buildWikiCitationModel([
    block('header', 1, [source({ citation_key: 'header' })], {
      block_type: 'table_row',
      content: '| Name | Value |',
      provenance_status: 'partial',
    }),
    block('delimiter', 2, [], {
      block_type: 'table_row',
      content: '| --- | --- |',
      provenance_status: 'unsupported',
    }),
    block('data', 3, [source({ citation_key: 'data', chunk_id: 'chunk-2' })], {
      block_type: 'table_row',
      content: '| Alpha | 1 |',
      provenance_status: 'verified',
    }),
  ])
  const entries = buildWikiProvenanceRenderEntries(model.blocks)

  assert.equal(entries.length, 1)
  assert.equal(entries[0]?.content, '| Name | Value |\n| --- | --- |\n| Alpha | 1 |')
  assert.deepEqual(
    entries[0]?.citationGroups.map((group) => ({
      key: group.key,
      position: group.position,
      status: group.provenanceStatus,
      authorType: group.authorType,
      structural: group.structural,
    })),
    [
      { key: 'header', position: 1, status: 'partial', authorType: 'pipeline', structural: false },
      { key: 'delimiter', position: 0, status: 'unsupported', authorType: 'pipeline', structural: true },
      { key: 'data', position: 2, status: 'verified', authorType: 'pipeline', structural: false },
    ],
  )
})

test('keeps ordered and unordered list provenance in separate render groups', () => {
  const model = buildWikiCitationModel([
    block('ordered-1', 1, [], { block_type: 'list_item', content: '1. First' }),
    block('ordered-2', 2, [], { block_type: 'list_item', content: '2. Second' }),
    block('unordered', 3, [], { block_type: 'list_item', content: '- Third' }),
  ])
  const entries = buildWikiProvenanceRenderEntries(model.blocks)

  assert.equal(entries.length, 2)
  assert.equal(entries[0]?.content, '1. First\n2. Second')
  assert.deepEqual(entries[0]?.citationGroups.map((group) => group.position), [1, 2])
  assert.equal(entries[1]?.content, '- Third')
})

test('marks headings as structural so they do not show unsupported evidence badges', () => {
  const model = buildWikiCitationModel([
    block('heading', 1, [], {
      block_type: 'heading',
      content: '## Overview',
      provenance_status: 'unsupported',
    }),
  ])

  const entries = buildWikiProvenanceRenderEntries(model.blocks)
  assert.equal(entries[0]?.citationGroups[0]?.structural, true)
})

test('hides pipeline layout badges but keeps user-authored source-less blocks visible', () => {
  const model = buildWikiCitationModel([
    block('divider', 1, [], {
      content: '---',
      author_type: 'pipeline',
      provenance_status: 'unsupported',
    }),
    block('manual', 2, [], {
      content: 'A person added this paragraph.',
      author_type: 'user',
      provenance_status: 'unsupported',
    }),
  ])

  const entries = buildWikiProvenanceRenderEntries(model.blocks)
  assert.equal(entries[0]?.citationGroups[0]?.structural, true)
  assert.equal(entries[1]?.citationGroups[0]?.structural, false)
  assert.equal(entries[1]?.citationGroups[0]?.authorType, 'user')
})
