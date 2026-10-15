import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createI18n } from 'vue-i18n'
import en from '../i18n/locales/en-US'
import { assistantDocURL, getAssistantToolDisplayType, getAssistantToolTitle, parseAssistantToolOutput } from './assistantToolDisplay'
import { getAgentToolIconName } from './agent-tool-icons'

const { t } = createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': en } }).global

test('all management and product documentation tools have dedicated display types and icons', () => {
  for (const name of ['find_operations', 'run_operation', 'search_weknora_docs', 'read_weknora_doc']) {
    assert.equal(getAssistantToolDisplayType(name), name)
    assert.notEqual(getAgentToolIconName(name), 'file-paste')
  }
  assert.equal(getAssistantToolDisplayType('toString'), undefined)
  assert.equal(getAssistantToolDisplayType('wiki_read_page'), undefined)
})

test('parsing supports output-only history and SSE envelopes without masking payload errors', () => {
  const output = JSON.stringify({ operations: [], error: 'No section found', outline: [{ heading: 'Setup' }] })
  const history = parseAssistantToolOutput(output)
  assert.deepEqual(parseAssistantToolOutput(output, { output, error: '', success: true }), history)
  assert.deepEqual(parseAssistantToolOutput('', { operations: [], hint: 'Narrow query' }), { operations: [], hint: 'Narrow query' })
  for (const invalid of ['null', '[]', '0', 'partial {', 'plain failure']) {
    assert.deepEqual(parseAssistantToolOutput(invalid), {})
  }
})

test('timeline titles show operation or document details in running, failed and historical states', () => {
  assert.equal(getAssistantToolTitle(t, { tool_name: 'find_operations', arguments: { domain: 'wiki' } }), 'Find operations: wiki')
  assert.equal(getAssistantToolTitle(t, { tool_name: 'run_operation', arguments: { operation: 'knowledge.delete' }, pending: true }), 'Calling Run operation: knowledge.delete...')
  assert.equal(getAssistantToolTitle(t, { tool_name: 'run_operation', arguments: '{"operation":"knowledge.delete"}', success: false }), 'Failed to call Run operation: knowledge.delete')
  assert.equal(getAssistantToolTitle(t, { tool_name: 'search_weknora_docs', arguments: { query: 'Wiki' } }), 'Search WeKnora docs: Wiki')
  assert.equal(getAssistantToolTitle(t, { tool_name: 'read_weknora_doc', output: '{"title":"Wiki","section":"Wiki > Setup"}' }), 'Read WeKnora doc: Wiki > Setup')
  assert.equal(getAssistantToolTitle(t, { tool_name: 'read_weknora_doc', arguments: { path: '03-features/wiki' } }), 'Read WeKnora doc: 03-features/wiki')
  assert.equal(getAssistantToolTitle(t, { tool_name: 'shell_exec' }), '')
})

test('documentation links allow only web URLs and safely replace section fragments', () => {
  for (const unsafe of ['javascript:alert(1)', 'data:text/html,test', '//evil.test', '/admin', '', null]) {
    assert.equal(assistantDocURL(unsafe), undefined)
  }
  assert.equal(assistantDocURL('https://weknora.example/docs/wiki#old', 'setup'), 'https://weknora.example/docs/wiki#setup')
})
