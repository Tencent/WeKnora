import assert from 'node:assert/strict'
import test from 'node:test'

import { buildImageProcessingConfig, type ImageProcessingEdits } from './imageProcessingConfig.ts'

const DEFAULT_ON = [
  { prop: 'contain.text', is: 'block' },
  { prop: 'contain.data_visual', is: 'true' },
]

/** The edits every test shares; `edits` overrides them. */
function editsWith(overrides: Partial<ImageProcessingEdits> = {}): ImageProcessingEdits {
  return {
    imageAttrsEnabled: true,
    onUnobserved: false,
    defaultOn: DEFAULT_ON,
    pipelineId: 'ob_cap_ocr',
    pipelineParams: {},
    ...overrides,
  }
}

test('saving keeps OCR conditions customised through the API', () => {
  // Regression: the editor always wrote the registry default into `on`, so an
  // unrelated edit replaced a KB's custom OCR conditions with the default table.
  const custom = [{ prop: 'contain.text', is: 'sparse' }]
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1', image_attrs_enabled: true, image_actions: { ocr: { on: custom, on_unobserved: true } } },
    editsWith(),
  )

  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: custom, on_unobserved: false } },
    image_pipeline: 'ob_cap_ocr',
  })
})

test('a KB without custom conditions gets the registry default alongside on_unobserved', () => {
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    editsWith(),
  )

  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
    image_pipeline: 'ob_cap_ocr',
  })
})

test('the chosen pipeline and its private parameters are written back', () => {
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    editsWith({ pipelineId: 'caption_ocr', pipelineParams: { enable_caption: false, enable_ocr: true } }),
  )

  // Both keys of this pipeline travel, and they are capitalised exactly as the
  // backend declares them: a misspelled key is silently ignored at run time.
  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
    image_pipeline: 'caption_ocr',
    image_pipeline_params: { enable_caption: false, enable_ocr: true },
  })
})

test('an unchanged configuration is not sent', () => {
  const custom = [{ prop: 'contain.text', is: 'sparse' }]
  const snapshot = {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: custom, on_unobserved: true } },
    image_pipeline: 'ob_cap_ocr',
  }

  assert.equal(
    buildImageProcessingConfig(snapshot, editsWith({ onUnobserved: true })),
    null,
  )
})

test('clearing the pick removes the stored one rather than repeating it', () => {
  // An empty pick means "resolve from image_attrs_enabled", which is what the
  // field already said by being absent, so saving has to actually drop it.
  const snapshot = {
    image_pipeline: 'ob_cap_ocr',
    image_pipeline_params: { capture_caption: true },
  }
  const built = buildImageProcessingConfig(snapshot, editsWith({ pipelineId: '', pipelineParams: {} }))

  assert.deepEqual(built, {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
  })
})
