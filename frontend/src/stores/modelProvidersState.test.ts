import assert from 'node:assert/strict'
import test from 'node:test'

import type { ModelProviderOption } from '@/api/initialization'

import {
  credentialLabelForModelType,
  extraFieldLabel,
  extraFieldOptionLabel,
  extraFieldPlaceholder,
  extraFieldsForModelType,
  loadProvidersForType,
  mergeProviderIndex,
  normalizeModelType,
  pickLocalized,
  providerDescription,
  providerIcon,
  providerLabel,
  sortProviders,
} from './modelProvidersState.ts'

/** loadProvidersForType logs failures; keep the test output readable. */
async function withoutConsoleError<T>(run: () => Promise<T>): Promise<T> {
  const original = console.error
  console.error = () => {}
  try {
    return await run()
  } finally {
    console.error = original
  }
}

const svgIcon = 'data:image/svg+xml;base64,PHN2Zy8+'

function provider(overrides: Partial<ModelProviderOption> & Pick<ModelProviderOption, 'value'>): ModelProviderOption {
  return {
    label: overrides.value,
    description: '',
    defaultUrls: {},
    modelTypes: ['chat'],
    ...overrides,
  }
}

test('providerLabel uses labels[locale], then the same language, then label', () => {
  const lkeap = provider({
    value: 'lkeap',
    label: 'Tencent Cloud LKEAP',
    labels: { 'zh-CN': '腾讯云 LKEAP' },
  })
  assert.equal(providerLabel(lkeap, 'zh-CN'), '腾讯云 LKEAP')
  assert.equal(providerLabel(lkeap, 'zh-TW'), '腾讯云 LKEAP', 'language match without exact region')
  assert.equal(providerLabel(lkeap, 'en-US'), 'Tencent Cloud LKEAP')
  assert.equal(providerLabel(lkeap, 'ja-JP'), 'Tencent Cloud LKEAP')
  assert.equal(providerLabel(lkeap), 'Tencent Cloud LKEAP')
  assert.equal(providerLabel(null, 'zh-CN'), '')
})

test('providerLabel falls back to the id when the vendor ships no label', () => {
  assert.equal(providerLabel(provider({ value: 'custom', label: '' }), 'en-US'), 'custom')
})

test('pickLocalized ignores empty localized strings', () => {
  assert.equal(pickLocalized({ 'zh-CN': '   ' }, 'zh-CN', 'fallback'), 'fallback')
  assert.equal(pickLocalized(undefined, 'zh-CN', 'fallback'), 'fallback')
  assert.equal(pickLocalized({ 'zh-CN': 'x' }, undefined, 'fallback'), 'fallback')
})

test('providerDescription mirrors the label lookup', () => {
  const p = provider({ value: 'x', description: 'gpt-4o, o3', descriptions: { 'zh-CN': 'gpt-4o、o3 等' } })
  assert.equal(providerDescription(p, 'zh-CN'), 'gpt-4o、o3 等')
  assert.equal(providerDescription(p, 'ru-RU'), 'gpt-4o, o3')
})

test('providerIcon only accepts data image URIs', () => {
  assert.equal(providerIcon(provider({ value: 'a', icon: svgIcon })), svgIcon)
  assert.equal(providerIcon(provider({ value: 'b' })), '')
  assert.equal(providerIcon(provider({ value: 'c', icon: 'https://evil.example/x.svg' })), '')
  assert.equal(providerIcon(undefined), '')
})

test('extraFieldsForModelType honours model_types in either naming scheme', () => {
  const fields = [
    { key: 'api_version', label: 'API Version', type: 'string' },
    { key: 'secret_key', label: 'Secret Key', type: 'password', model_types: ['Rerank'], secret: true },
    { key: 'region', label: 'Region', type: 'string', model_types: ['rerank'] },
    { key: '', label: 'broken', type: 'string' },
  ]
  assert.deepEqual(extraFieldsForModelType(fields, 'rerank').map((f) => f.key), ['api_version', 'secret_key', 'region'])
  assert.deepEqual(extraFieldsForModelType(fields, 'chat').map((f) => f.key), ['api_version'])
  assert.deepEqual(extraFieldsForModelType(fields, 'KnowledgeQA').map((f) => f.key), ['api_version'])
  assert.deepEqual(extraFieldsForModelType(undefined, 'chat'), [])
})

test('normalizeModelType maps backend names onto editor names', () => {
  assert.equal(normalizeModelType('KnowledgeQA'), 'chat')
  assert.equal(normalizeModelType('VLLM'), 'vllm')
  assert.equal(normalizeModelType('embedding'), 'embedding')
  assert.equal(normalizeModelType(''), '')
})

test('extraFieldLabel localizes with the same fallback chain', () => {
  const field = { key: 'region', label: 'Region', labels: { 'zh-CN': '地域' }, type: 'string' }
  assert.equal(extraFieldLabel(field, 'zh-CN'), '地域')
  assert.equal(extraFieldLabel(field, 'en-US'), 'Region')
  assert.equal(extraFieldLabel({ key: 'k', label: '', type: 'string' }), 'k')
})

test('sortProviders orders by backend order then label', () => {
  const sorted = sortProviders([
    provider({ value: 'generic', label: 'Custom', order: 99 }),
    provider({ value: 'openai', label: 'OpenAI', order: 30 }),
    provider({ value: 'zeta', label: 'Zeta' }),
    provider({ value: 'aliyun', label: 'Aliyun', order: 30 }),
  ])
  assert.deepEqual(sorted.map((p) => p.value), ['aliyun', 'openai', 'generic', 'zeta'])
})

test('sortProviders survives rows with neither label nor value', () => {
  const sorted = sortProviders([
    { description: '', defaultUrls: {}, modelTypes: [] } as unknown as ModelProviderOption,
    provider({ value: 'openai', label: 'OpenAI' }),
  ])
  assert.equal(sorted.length, 2)
})

test('loadProvidersForType caches a successful list, sorted and id-filtered', async () => {
  const seen: Array<string | undefined> = []
  const result = await loadProvidersForType(async (type) => {
    seen.push(type)
    return [
      provider({ value: 'zeta', label: 'Zeta' }),
      { value: '', label: 'broken' },
      null,
      provider({ value: 'openai', label: 'OpenAI', order: 1 }),
    ]
  }, 'chat')
  assert.deepEqual(seen, ['chat'])
  assert.equal(result.cacheable, true)
  assert.deepEqual(result.providers.map((p) => p.value), ['openai', 'zeta'])
})

test('loadProvidersForType passes undefined for the all-types key', async () => {
  const seen: Array<string | undefined> = []
  await loadProvidersForType(async (type) => { seen.push(type); return [] }, '')
  assert.deepEqual(seen, [undefined])
})

test('loadProvidersForType caches a genuinely empty catalog', async () => {
  const result = await loadProvidersForType(async () => [], 'rerank')
  assert.deepEqual(result, { providers: [], cacheable: true })
})

test('loadProvidersForType never caches a failed or malformed response', async () => {
  await withoutConsoleError(async () => {
    const failed = await loadProvidersForType(async () => { throw new Error('502') }, 'chat')
    assert.deepEqual(failed, { providers: [], cacheable: false }, 'a transient failure must be retried')
  })
  for (const malformed of [null, undefined, { data: [] }, 'nope']) {
    const result = await loadProvidersForType(async () => malformed, 'chat')
    assert.deepEqual(result, { providers: [], cacheable: false })
  }
})

test('mergeProviderIndex keeps catalog models collected across model types', () => {
  const chat = provider({ value: 'openai', models: [{ id: 'gpt-4o', name: 'GPT-4o', type: 'chat' }] })
  const embedding = provider({
    value: 'openai',
    models: [
      { id: 'text-embedding-3-large', name: 'Embedding 3 Large', type: 'embedding' },
      { id: 'gpt-4o', name: 'GPT-4o', type: 'chat' },
    ],
  })
  const index = mergeProviderIndex(mergeProviderIndex({}, [chat]), [embedding])
  assert.deepEqual(index.openai.models?.map((m) => m.id), ['gpt-4o', 'text-embedding-3-large'])
  assert.equal(Object.keys(index).length, 1)
})

test('mergeProviderIndex tolerates missing values and model lists', () => {
  const base = mergeProviderIndex({}, [
    provider({ value: 'openai' }),
    { label: 'no id' } as unknown as ModelProviderOption,
  ])
  assert.deepEqual(Object.keys(base), ['openai'])
  // A later fetch without `models` must not wipe what an earlier one collected.
  const withModels = mergeProviderIndex(
    mergeProviderIndex({}, [provider({ value: 'openai', models: [{ id: 'gpt-4o', name: 'GPT-4o', type: 'chat' }] })]),
    [provider({ value: 'openai', label: 'OpenAI' })],
  )
  assert.deepEqual(withModels.openai.models?.map((m) => m.id), ['gpt-4o'])
  assert.equal(withModels.openai.label, 'OpenAI', 'fresh metadata still wins')
})

test('credentialLabelForModelType follows the vendor declaration, not a hardcoded table', () => {
  const labels = [
    {
      label: 'SecretId',
      labels: { 'zh-CN': 'SecretId（TC3 签名）' },
      placeholder: 'AKID...',
      hint: 'not an sk- key',
      model_types: ['Rerank'],
      required: true,
    },
  ]
  // The signed rerank API renames the field; chat on the same vendor does not.
  const rerank = credentialLabelForModelType(labels, 'rerank')
  assert.equal(rerank?.label, 'SecretId')
  assert.equal(rerank?.required, true)
  assert.equal(credentialLabelForModelType(labels, 'chat'), null)
  assert.equal(credentialLabelForModelType(labels, 'embedding'), null)

  // Backend spelling of the model type resolves the same way.
  assert.equal(credentialLabelForModelType(labels, 'Rerank')?.label, 'SecretId')

  // Vendors that take a plain API key declare nothing.
  assert.equal(credentialLabelForModelType(undefined, 'rerank'), null)
  assert.equal(credentialLabelForModelType([], 'rerank'), null)

  // An entry without a model_types filter applies to every type.
  const everywhere = [{ label: 'Access Key' }]
  assert.equal(credentialLabelForModelType(everywhere, 'chat')?.label, 'Access Key')

  // Localization goes through the same picker as every other vendor string.
  assert.equal(pickLocalized(rerank?.labels, 'zh-CN', rerank!.label), 'SecretId（TC3 签名）')
  assert.equal(pickLocalized(rerank?.labels, 'en-US', rerank!.label), 'SecretId')
})

// Select options and placeholders used to render their raw English string.
// That was invisible while every option was an identifier — LKEAP's region
// codes — and became a gap as soon as one was prose.
test('extraFieldOptionLabel resolves the locale, falling back to label then value', () => {
  const option = {
    label: '0..1 relevance (BGE class)',
    labels: { 'zh-CN': '0~1 相关度（BGE 一类）' },
    value: 'probability',
  }
  assert.equal(extraFieldOptionLabel(option, 'zh-CN'), '0~1 相关度（BGE 一类）')
  assert.equal(extraFieldOptionLabel(option, 'en'), '0..1 relevance (BGE class)')
  assert.equal(
    extraFieldOptionLabel({ label: 'ap-guangzhou', value: 'ap-guangzhou' }, 'zh-CN'),
    'ap-guangzhou',
  )
  assert.equal(extraFieldOptionLabel({ value: 'logit' }, 'zh-CN'), 'logit')
})

test('extraFieldPlaceholder resolves the locale and tolerates no placeholder', () => {
  const field = {
    key: 'score_scale',
    label: 'Rerank score scale',
    type: 'select',
    placeholder: 'match the reranker actually deployed behind this endpoint',
    placeholders: { 'zh-CN': '按这个端点后面实际部署的重排模型选择' },
  }
  assert.equal(extraFieldPlaceholder(field, 'zh-CN'), '按这个端点后面实际部署的重排模型选择')
  assert.equal(
    extraFieldPlaceholder(field, 'en'),
    'match the reranker actually deployed behind this endpoint',
  )
  assert.equal(extraFieldPlaceholder({ key: 'k', label: 'k', type: 'string' }, 'zh-CN'), '')
})
