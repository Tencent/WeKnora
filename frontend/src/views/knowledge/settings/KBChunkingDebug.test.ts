import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive } from 'vue'
import { fixture } from '../../../test-utils/vueFixture.ts'
import type { PreviewChunkingRequest, PreviewChunkingResponse } from '../../../types/chunker.ts'

const output: PreviewChunkingResponse = {
  selected_tier: 'heading', tier_chain: ['heading'], rejected: [],
  profile: {
    total_chars: 12, total_lines: 2, avg_line_len: 6, std_line_len: 0,
    md_heading_counts: { '1': 1 }, md_heading_total: 1, numbered_section_count: 0,
    all_caps_short_line_count: 0, blank_paragraph_breaks: 0, form_feed_count: 0,
    visual_sep_count: 0, german_chapter_count: 0, english_chapter_count: 0,
    chinese_chapter_count: 0, repeated_footer_count: 0, has_tables: false,
    has_code: false, code_ratio: 0, detected_langs: ['en'],
  },
  chunks: [{ seq: 0, start: 0, end: 12, size_chars: 12, size_tokens_approx: 3, content: '# Title\nText' }],
  stats: { count: 1, avg_chars: 12, min_chars: 12, max_chars: 12, stddev_chars: 0 },
}

function mount(t: TestContext, previewChunking: (input: PreviewChunkingRequest) => Promise<unknown>) {
  const config = reactive({
    chunkSize: 512, chunkOverlap: 50, separators: ['\n\n'], enableParentChild: false,
    parentChunkSize: 1024, childChunkSize: 256, strategy: 'auto', tokenLimit: 256,
    languages: ['en'],
  })
  const errors = t.mock.fn()
  const { vm } = fixture(t).component(new URL('./KBChunkingDebug.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: errors } },
    'tdesign-icons-vue-next': {},
    '@/api/chunker': { previewChunking },
    './chunkingSamples': { CHUNKING_SAMPLES: [{ id: 'preset', text: 'Preset text' }], DEFAULT_SAMPLE_ID: 'preset' },
  }, { config })
  return { vm, config, errors }
}

test('assistant analysis keeps the input and parameters that produced the visible preview', async t => {
  let complete!: (value: unknown) => void
  const previewChunking = t.mock.fn((_input: PreviewChunkingRequest) => new Promise(resolve => { complete = resolve }))
  const { vm, config } = mount(t, previewChunking)
  vm.sample = '# Title\nText'
  const pending = vm.runPreview()
  assert.equal(vm.assistantPreviewSelection, undefined)

  // The form remains editable while the request is pending and after it finishes.
  vm.sample = 'Unpreviewed draft'
  config.chunkSize = 2048
  config.separators.push('.')
  config.languages.push('zh')
  const submitted = previewChunking.mock.calls[0].arguments[0]
  assert.equal(submitted.text, '# Title\nText')
  assert.equal(submitted.chunking_config.chunk_size, 512)
  assert.deepEqual(Array.from(submitted.chunking_config.separators), ['\n\n'])
  assert.deepEqual(Array.from(submitted.chunking_config.languages!), ['en'])

  complete({ success: true, data: output })
  await pending
  const selection = vm.assistantPreviewSelection
  assert.match(selection.text, /Original text excerpt:\n# Title\nText/)
  assert.match(selection.text, /"chunk_size":512/)
  assert.match(selection.text, /Chunk #0 \[0–12\]/)
  assert.doesNotMatch(selection.text, /Unpreviewed draft|2048/)
  vm.sample = 'Another draft'
  config.chunkOverlap = 100
  assert.equal(vm.assistantPreviewSelection.text, selection.text)
})

test('assistant context is cleared for a new or failed preview and when loading a preset', async t => {
  let complete!: (value: unknown) => void
  let fail!: (reason: Error) => void
  const { vm, errors } = mount(t, () => new Promise((resolve, reject) => { complete = resolve; fail = reject }))
  t.mock.method(console, 'error', () => {})
  vm.sample = 'First document'
  let pending = vm.runPreview()
  complete({ success: true, data: output })
  await pending
  assert.ok(vm.assistantPreviewSelection)

  pending = vm.runPreview()
  assert.equal(vm.assistantPreviewSelection, undefined)
  fail(new Error('Preview unavailable'))
  await pending
  assert.equal(vm.assistantPreviewSelection, undefined)
  assert.equal(vm.error, 'Preview unavailable')
  assert.equal(errors.mock.callCount(), 1)

  pending = vm.runPreview()
  complete({ success: true, data: output })
  await pending
  vm.loadSample('preset')
  assert.equal(vm.sample, 'Preset text')
  assert.equal(vm.assistantPreviewSelection, undefined)
})

test('large previews retain both original and chunk excerpts within the page-selection budget', async t => {
  const chunks = Array.from({ length: 10 }, (_, seq) => ({
    seq, start: seq * 2000, end: (seq + 1) * 2000, size_chars: 2000, size_tokens_approx: 500,
    content: `Chunk ${seq} content ` + '文'.repeat(2000),
  }))
  const { vm } = mount(t, async () => ({ success: true, data: { ...output, chunks } }))
  vm.sample = 'Original text ' + '原'.repeat(10000)
  await vm.runPreview()
  const text = vm.assistantPreviewSelection.text
  assert.ok(text.length <= 4000)
  assert.match(text, /not the full document/)
  assert.match(text, /Original text excerpt:\nOriginal text/)
  assert.match(text, /"chunk_size":512/)
  assert.match(text, /Statistics:/)
  for (let seq = 0; seq < 3; seq++) assert.ok(text.includes(`Chunk ${seq} content`))
  assert.doesNotMatch(text, /Chunk 3 content/)
})
