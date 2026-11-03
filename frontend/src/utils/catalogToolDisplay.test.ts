import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { createI18n, type ComposerTranslation } from 'vue-i18n'
import { LOCALE_BUNDLES } from '../i18n/localeKeyAudit'
import {
  docHits,
  docPages,
  docReadView,
  getCatalogToolDisplayType,
  getCatalogToolTitle,
  getOperationLabel,
  operationRows,
  parseCatalogPayload,
  runOperationView,
  safeDocUrl,
  unavailableOperationRows,
} from './catalogToolDisplay'

const t = ((key: string, params?: Record<string, unknown>) => {
  if (params && 'name' in params) return `${key} ${params.name}`
  return key
}) as ComposerTranslation

test('operation titles follow the UI locale for live events, history, and status changes', () => {
  const { global } = createI18n({ legacy: false, locale: 'zh-CN', messages: LOCALE_BUNDLES })
  const event = { tool_name: 'run_operation', arguments: { operation: 'model.list' } }
  assert.equal(getCatalogToolTitle(global.t, event), '执行操作：获取模型列表')
  assert.equal(getCatalogToolTitle(global.t, {
    tool_name: 'run_operation', arguments: { operation: 'automation.validate_trigger' },
  }), '执行操作：校验运行时间')
  assert.equal(getCatalogToolTitle(global.t, {
    tool_name: 'run_operation', output: JSON.stringify({ operation: 'agent.list' }),
  }), '执行操作：获取智能体列表')
  for (const status of [{ pending: true }, { success: false }]) {
    assert.match(getCatalogToolTitle(global.t, { ...event, ...status }), /获取模型列表/)
  }
  global.locale.value = 'en-US'
  assert.equal(getCatalogToolTitle(global.t, event), 'Run operation：List models')
  assert.equal(getOperationLabel(global.t, 'future.unknown'), 'future.unknown')
  assert.equal(getOperationLabel(global.t, ''), '')
  assert.equal(event.arguments.operation, 'model.list')
})

test('every backend catalog operation has a label in every UI locale', () => {
  const spec = JSON.parse(readFileSync(new URL('../../../docs/swagger.json', import.meta.url), 'utf8'))
  const ids = new Set<string>()
  for (const path of Object.values(spec.paths)) {
    for (const op of Object.values(path as Record<string, any>)) {
      if (op['x-agent']?.id) ids.add(op['x-agent'].id)
    }
  }
  assert.ok(ids.size > 0)
  for (const [locale, bundle] of Object.entries(LOCALE_BUNDLES)) {
    const { global } = createI18n({ legacy: false, locale, fallbackLocale: false, messages: { [locale]: bundle } })
    for (const id of ids) {
      assert.ok(global.te(`agentStream.operations.names.${id}`), `${locale}: missing ${id}`)
      assert.notEqual(getOperationLabel(global.t, id), id, `${locale}: untranslated ${id}`)
    }
  }
})

test('operation and product-doc tools have dedicated display types', () => {
  assert.equal(getCatalogToolDisplayType('find_operations'), 'find_operations')
  assert.equal(getCatalogToolDisplayType('run_operation'), 'run_operation')
  assert.equal(getCatalogToolDisplayType('search_weknora_docs'), 'search_weknora_docs')
  assert.equal(getCatalogToolDisplayType('read_weknora_doc'), 'read_weknora_doc')
  assert.equal(getCatalogToolDisplayType('web_search'), undefined)
})

test('catalog payload prefers the output prop over the live event envelope', () => {
  const output = JSON.stringify({ operations: [{ id: 'knowledge.get', risk: 'read', summary: 'Get one' }] })
  const data = parseCatalogPayload(output, {
    tool_name: 'find_operations', success: true, output: '{"operations":[]}', data_change: { operation: 'x' },
  })
  assert.equal(operationRows(data)[0]?.id, 'knowledge.get')
  assert.equal('data_change' in data, false)
})

test('titles name the operation, query, or page instead of the raw tool id', () => {
  assert.equal(
    getCatalogToolTitle(t, { tool_name: 'run_operation', arguments: { operation: 'knowledge.get' } }),
    'agentStream.tools.runOperation：knowledge.get',
  )
  assert.equal(
    getCatalogToolTitle(t, { tool_name: 'search_weknora_docs', arguments: { query: 'Wiki' }, pending: true }),
    'agentStream.toolStatus.calling agentStream.tools.searchWeKnoraDocs：「Wiki」',
  )
  assert.equal(
    getCatalogToolTitle(t, {
      tool_name: 'read_weknora_doc', success: false, arguments: { path: '03-features/14-wiki' },
    }),
    'agentStream.toolStatus.calledFailed agentStream.tools.readWeKnoraDoc：「03-features/14-wiki」',
  )
  assert.equal(getCatalogToolTitle(t, { tool_name: 'web_search' }), '')
})

test('operation rows keep parameters and withheld reasons, and runs keep projected data', () => {
  const found = parseCatalogPayload(JSON.stringify({
    operations: [{
      id: 'knowledge.delete', risk: 'destructive', summary: 'Delete',
      description: 'Removes a document',
      params: [{ name: 'knowledge_id', type: 'string', required: true, description: 'Document', enum: ['a'], minimum: 1, maximum: 10 }],
    }],
    unavailable: [{ id: 'agent.create', risk: 'write', summary: 'Create', reason: 'read-only' }],
    hint: 'for the model',
  }))
  const row = operationRows(found)[0]
  assert.equal(row?.expandable, true)
  assert.equal(row?.params[0]?.required, true)
  assert.equal(row?.params[0]?.enumText, 'a')
  assert.deepEqual(row?.params[0]?.range, { min: 1, max: 10 })
  assert.equal(unavailableOperationRows(found)[0]?.reason, 'read-only')

  const run = runOperationView(parseCatalogPayload(JSON.stringify({
    operation: 'knowledge.list',
    result: { data: [{ title: '手册' }], meta: { total: 1 }, truncated: true },
  })))
  assert.equal(run.operation, 'knowledge.list')
  assert.equal(run.truncated, true)
  assert.deepEqual(run.meta, { total: 1 })
})

test('doc search, read, and links stay structured', () => {
  const listed = parseCatalogPayload(JSON.stringify({
    pages: [{ path: '03-features/14-wiki', title: 'Wiki', category: '03-features', url: 'https://docs.example/wiki' }],
  }))
  assert.equal(docPages(listed)[0]?.title, 'Wiki')
  const hits = docHits(parseCatalogPayload(JSON.stringify({
    results: [{ path: '03-features/14-wiki', anchor: 'setup', section: '安装', url: 'javascript:alert(1)', snippet: '<script>' }],
  })))
  assert.equal(hits[0]?.section, '安装')
  assert.equal(safeDocUrl(hits[0]?.url || ''), '')
  assert.equal(safeDocUrl('https://docs.example/wiki'), 'https://docs.example/wiki')
  const read = docReadView(parseCatalogPayload(JSON.stringify({
    title: 'Wiki', path: '03-features/14-wiki', content: '# 安装\n\n正文', outline: [{ anchor: 'setup', heading: '安装', level: 2 }],
    truncated: true,
  })))
  assert.equal(read.outline[0]?.heading, '安装')
  assert.equal(read.truncated, true)
})
