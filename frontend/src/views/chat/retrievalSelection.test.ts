import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')
const start = source.indexOf('const hostScope = props.embeddedMode')
const end = source.indexOf('const mcpServiceIds =', start)
assert.ok(start > 0 && end > start)

test('assistant page context does not replace or widen explicit KB, file and tag mentions', () => {
  for (const mentionedItems of [
    [],
    [{ id: 'selected-kb', type: 'kb' }],
    [{ id: 'doc', type: 'file' }],
    [{ id: 'tag', type: 'tag', kb_id: 'selected-kb' }],
  ]) {
    const result = runInNewContext(`${source.slice(start, end)}; ({ kbIds, knowledgeIds, tagIds, hostScope });`, {
      props: { embeddedMode: true, hostScope: () => ({ knowledgeBaseIds: [], pageContext: { knowledge_base_id: 'page-kb' } }) },
      useSettingsStoreInstance: { settings: { selectedKnowledgeBases: ['unrelated-sidebar-kb'], selectedFiles: ['unrelated-doc'] } },
      pendingSuggestionKnowledgeBaseIds: [], mentionedItems,
    })
    assert.deepEqual(Array.from(result.kbIds), mentionedItems.filter(i => i.type === 'kb').map(i => i.id))
    assert.deepEqual(Array.from(result.knowledgeIds), mentionedItems.filter(i => i.type === 'file').map(i => i.id))
    assert.deepEqual(Array.from(result.tagIds), mentionedItems.filter(i => i.type === 'tag').map(i => i.id))
    assert.equal(result.hostScope.pageContext.knowledge_base_id, 'page-kb')
  }
})
