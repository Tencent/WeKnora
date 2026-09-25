import assert from 'node:assert/strict'
import test from 'node:test'

import { parseWikiToolReferences } from './wikiToolReferences.ts'

test('parses wiki search hits into individual drawer references', () => {
  const refs = parseWikiToolReferences('wiki_search', `
    <search_results count="2" query="RAG">
      <page>
        <knowledge_base_id>kb-1</knowledge_base_id>
        <link>[[concept/rag|Retrieval-Augmented Generation]]</link>
        <type>concept</type>
        <summary>Grounds answers in retrieved evidence.</summary>
        <match_snippet>RAG combines retrieval and generation.</match_snippet>
      </page>
      <page>
        <knowledge_base_id>kb-2</knowledge_base_id>
        <link>[[summary/search]]</link>
        <summary>Search overview.</summary>
      </page>
    </search_results>
  `, 'call-1')

  assert.deepEqual(refs, [
    {
      id: 'call-1:kb-1:concept/rag',
      title: 'Retrieval-Augmented Generation',
      content: 'Grounds answers in retrieved evidence.\n\nRAG combines retrieval and generation.',
      knowledgeBaseId: 'kb-1',
      slug: 'concept/rag',
    },
    {
      id: 'call-1:kb-2:summary/search',
      title: 'summary/search',
      content: 'Search overview.',
      knowledgeBaseId: 'kb-2',
      slug: 'summary/search',
    },
  ])
})

test('parses wiki page reads and keeps markdown content', () => {
  const refs = parseWikiToolReferences('wiki_read_page', `
    <wiki_page>
      <metadata>
        <knowledge_base_id>kb-1</knowledge_base_id>
        <link>[[concept/rag|RAG]]</link>
      </metadata>
      <summary>Short summary.</summary>
      <content># RAG\n\nFull **Markdown** body.</content>
    </wiki_page>
  `, 'call-2')

  assert.equal(refs.length, 1)
  assert.equal(refs[0]?.title, 'RAG')
  assert.equal(refs[0]?.slug, 'concept/rag')
  assert.equal(refs[0]?.knowledgeBaseId, 'kb-1')
  assert.equal(refs[0]?.content, '# RAG\n\nFull **Markdown** body.')
})

test('uses the wiki summary only when a page has no body', () => {
  const refs = parseWikiToolReferences('wiki_read_page', `
    <wiki_page>
      <metadata><link>[[summary/empty|Empty page]]</link></metadata>
      <summary>Summary fallback.</summary>
      <content></content>
    </wiki_page>
  `, 'call-3')

  assert.equal(refs[0]?.content, 'Summary fallback.')
})

test('ignores non-wiki tools and empty result sets', () => {
  assert.deepEqual(parseWikiToolReferences('web_search', '<page>ignored</page>'), [])
  assert.deepEqual(parseWikiToolReferences('wiki_search', '<search_results count="0" />'), [])
})
