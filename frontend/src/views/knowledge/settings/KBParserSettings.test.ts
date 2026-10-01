import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'

const component = readFileSync(new URL('./KBParserSettings.vue', import.meta.url), 'utf8')
const script = component.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)![1]
const source = ts.createSourceFile('settings.ts', script, ts.ScriptTarget.Latest, true)
const names = new Set([
  'getEngineForGroup', 'getRuleForGroup', 'handleEngineChange',
  'handleDOCXIncludeHeadersChange', 'buildCompleteRules',
])
const functions = source.statements
  .filter(node => ts.isFunctionDeclaration(node) && names.has(node.name?.text || ''))
  .map(node => node.getText(source)).join('\n')
const code = ts.transpileModule(functions, {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

function settings() {
  const emitted: any[] = []
  const context: any = {
    localEngineRules: { value: [{ file_types: ['docx'], engine: 'builtin' }] },
    fileTypeGroups: { value: [{ extensions: ['docx'] }, { extensions: ['pdf'] }] },
    getDefaultEngine: () => 'builtin',
    emit: (_event: string, rules: any[]) => emitted.push(rules),
  }
  runInNewContext(code, context)
  return { context, emitted }
}

test('DOCX header preference survives engine changes and saving complete rules', () => {
  const { context, emitted } = settings()
  context.handleDOCXIncludeHeadersChange(['docx'], true)
  assert.equal(emitted.at(-1)[0].docx_include_headers, true)
  assert.equal(emitted.at(-1)[1].docx_include_headers, undefined)
  for (const engine of ['markitdown', 'anydoc', 'builtin']) {
    context.handleEngineChange(['docx'], engine)
    const rule = context.buildCompleteRules()[0]
    assert.equal(rule.engine, engine)
    assert.equal(rule.docx_include_headers, true)
  }
  context.handleDOCXIncludeHeadersChange(['docx'], false)
  assert.equal(emitted.at(-1)[0].docx_include_headers, false)
})

test('saving unchanged parser defaults does not enable DOCX headers', () => {
  const { context } = settings()
  assert.equal(context.buildCompleteRules()[0].docx_include_headers, undefined)
})
