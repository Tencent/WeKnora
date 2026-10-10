import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { isKnowledgeBaseReady } from '../../utils/knowledgeBaseReady.ts'

function declaration(file, name) {
  const vue = readFileSync(new URL(file, import.meta.url), 'utf8')
  const script = vue.match(/<script[^>]*>([\s\S]*?)<\/script>/)[1]
  const parsed = ts.createSourceFile(file, script, ts.ScriptTarget.Latest, true)
  const statement = parsed.statements.find(item => ts.isVariableStatement(item) && item.declarationList.declarations.some(d => d.name.getText(parsed) === name))
  assert.ok(statement, `missing handler ${name}`)
  return ts.transpile(`${statement.getText(parsed)}\n${name}`)
}

for (const name of ['handleCardClick', 'handleSharedKbClick', 'handleSharedKbClickFromAll', 'goToSharedKbFromPanel']) {
  test(`${name} opens unconfigured details without opening settings`, () => {
    const opened = []
    const handle = vm.runInNewContext(declaration('./KnowledgeBaseList.vue', name), {
      pins: { touchRecent() {} }, goDetail: id => opened.push(id),
      currentSharedKbForDetail: { value: { knowledge_base: { id: 'unconfigured' } } },
      closeSharedDetailPanel() {}, uiStore: { openKBSettings: () => assert.fail('settings must not open') },
    })
    handle({ id: 'unconfigured', knowledge_base: { id: 'unconfigured' } })
    assert.deepEqual(opened, ['unconfigured'])
  })
}

test('unconfigured details still reject uploads with setup guidance', () => {
  const warnings = []
  const guard = vm.runInNewContext(declaration('./KnowledgeBase.vue', 'ensureDocumentKbReady'), {
    isFAQ: { value: false }, kbId: { value: 'unconfigured' }, kbInfo: { value: {} },
    isKnowledgeBaseReady, missingStorageEngine: { value: false }, t: key => key,
    MessagePlugin: { warning: key => warnings.push(key) },
  })
  assert.equal(guard(), false)
  assert.deepEqual(warnings, ['knowledgeBase.notInitialized'])
})
