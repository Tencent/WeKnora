import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')
const condition = source.match(/<p v-if="([^"]+)" class="parser-hint"/)?.[1]

function shouldShowParserHint(canEdit: boolean, unsupportedFileTypes: string[]): boolean {
  assert.ok(condition, 'parser hint has a render condition')
  return Boolean(new Function('canEdit', 'unsupportedFileTypes', `return ${condition}`)(canEdit, unsupportedFileTypes))
}

test('read-only users do not see the parser-engine warning (#4486)', () => {
  assert.equal(shouldShowParserHint(false, ['pdf']), false)
})

test('editable users still see the parser-engine warning when needed (#4486)', () => {
  assert.equal(shouldShowParserHint(true, ['pdf']), true)
})
