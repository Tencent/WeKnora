import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'

const source = readFileSync(new URL('./ImageEvidenceEditor.vue', import.meta.url), 'utf8')
  .split('<script setup lang="ts">')[1].split('</script>')[0]
const compiled = ts.transpileModule(`${source}\nexport { beginEdit, save, draft, chunk, error, editing }`, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture() {
  const row = { id: 'ocr', knowledge_id: 'doc', chunk_type: 'image_ocr', content: 'original',
    content_revision: 3, image_info: '[{"url":"local://chart.png"}]', index_status: 'ready' }
  const props = vue.reactive({ image: { id: 'asset', knowledge_id: 'doc', url: 'local://chart.png' }, field: 'ocr_text' })
  const requests: string[] = [], writes: any[] = [], events: string[] = [], watches: Array<() => void> = []
  const unmounts: Array<() => void> = []
  let fail = false, conflict = false
  const state: any = {}
  vm.runInNewContext(compiled, {
    exports: state, defineProps: () => props, defineEmits: () => (name: string) => events.push(name),
    require(id: string) {
      if (id === 'vue') return { ...vue, watch: (_: unknown, fn: () => void) => watches.push(fn),
        onBeforeUnmount: (fn: () => void) => unmounts.push(fn) }
      if (id === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (id === '@/utils/request') return { get: async (url: string) => {
        requests.push(url)
        return { data: requests.length === 1 ? Array.from({ length: 100 }, (_, i) => ({
          ...row, id: `other-${i}`, image_info: '[{"url":"local://other.png"}]',
        })) : [row], total: 101 }
      } }
      if (id === '@/api/knowledge-base') return {
        getChunkByIdOnly: async () => ({ data: { ...row } }),
        updateDocumentChunk: async (knowledge: string, id: string, payload: unknown) => {
          writes.push({ knowledge, id, payload })
          if (conflict) throw { status: 409, message: 'conflict' }
          return { data: { ...row, content_revision: 4, index_status: fail ? 'failed' : 'ready' } }
        },
      }
      throw new Error(`Unexpected import ${id}`)
    },
    Error, URLSearchParams, JSON, console,
  })
  return { state, props, requests, writes, events, row, watches, unmounts,
    fail: () => { fail = true }, conflict: () => { conflict = true } }
}

test('loads the matching image evidence through pagination, then saves with its current revision', async () => {
  const f = fixture()
  await f.state.beginEdit()
  assert.equal(f.requests.length, 2)
  assert.match(f.requests[1], /page=2/)
  assert.match(f.requests[0], /chunk_type=image_ocr/)
  f.state.draft.value = 'corrected OCR'
  await f.state.save()
  assert.equal(f.writes[0].knowledge, 'doc')
  assert.equal(f.writes[0].id, 'ocr')
  assert.equal(f.writes[0].payload.expected_revision, 3)
  assert.equal(f.writes[0].payload.content, 'corrected OCR')
  assert.deepEqual(f.events, ['saved'])
})

test('does not report failed index synchronization as a successful save', async () => {
  const f = fixture()
  await f.state.beginEdit()
  f.fail()
  f.state.draft.value = 'correction'
  await f.state.save()
  assert.equal(f.events.length, 0)
  assert.equal(f.state.chunk.value.content_revision, 4)
  assert.equal(f.state.editing.value, true)
  assert.equal(f.state.error.value, 'knowledgeBase.chunkSavedIndexFailed')
})

test('a revision conflict requires a fresh read before another save', async () => {
  const f = fixture()
  await f.state.beginEdit()
  f.conflict()
  f.state.draft.value = 'correction'
  await f.state.save()
  assert.equal(f.state.chunk.value, null)
  await f.state.save()
  assert.equal(f.writes.length, 1)
  assert.equal(f.events.length, 0)
})

test('empty content never reaches the write API', async () => {
  const f = fixture()
  await f.state.beginEdit()
  f.state.draft.value = '   '
  await f.state.save()
  assert.equal(f.writes.length, 0)
  assert.equal(f.state.error.value, 'knowledgeBase.chunkContentRequired')
})

test('navigation clears the editor so the previous image cannot be saved under the next one', async () => {
  const f = fixture()
  await f.state.beginEdit()
  f.props.image.knowledge_id = 'next-doc'
  f.watches.forEach(fn => fn())
  await f.state.save()
  assert.equal(f.writes.length, 0)
})

test('a late load after navigation cannot reopen the previous image editor', async () => {
  const f = fixture()
  const pending = f.state.beginEdit()
  f.props.image.id = 'next-image'
  f.watches.forEach(fn => fn())
  await pending
  assert.equal(f.state.chunk.value, null)
  assert.equal(f.state.editing.value, false)
})

test('a late save completion after navigation cannot reload or close the next image', async () => {
  const f = fixture()
  await f.state.beginEdit()
  f.state.draft.value = 'correction'
  const pending = f.state.save()
  f.props.image.id = 'next-image'
  f.watches.forEach(fn => fn())
  await pending
  assert.equal(f.events.length, 0)
  assert.equal(f.state.chunk.value, null)
})
