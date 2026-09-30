import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildImageProcessingConfig,
  buildPipelineFields,
  resolveImagePipelineFromKb,
  normalizeImagePipelineId,
  IMAGE_PIPELINE_SMARTOCR,
  LEGACY_PIPELINE_CAPTION_OCR,
  LEGACY_PIPELINE_OB_CAP_OCR,
  type ImageProcessingEdits,
} from './imageProcessingConfig.ts'

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
    pipelineId: IMAGE_PIPELINE_SMARTOCR,
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
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
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
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  })
})

test('the chosen pipeline and its private parameters are written back', () => {
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    editsWith({ pipelineId: LEGACY_PIPELINE_CAPTION_OCR, pipelineParams: { enable_caption: false, enable_ocr: true } }),
  )

  // Both keys of this pipeline travel, and they are capitalised exactly as the
  // backend declares them: a misspelled key is silently ignored at run time.
  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
    image_pipeline: LEGACY_PIPELINE_CAPTION_OCR,
    image_pipeline_params: { enable_caption: false, enable_ocr: true },
  })
})

test('an unchanged configuration is not sent', () => {
  const custom = [{ prop: 'contain.text', is: 'sparse' }]
  const snapshot = {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: custom, on_unobserved: true } },
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  }

  assert.equal(
    buildImageProcessingConfig(snapshot, editsWith({ onUnobserved: true })),
    null,
  )
})

test('clearing the pick removes the stored one rather than repeating it', () => {
  // An empty pick means "resolve from image_attrs_enabled", which is what the
  // field already said by being absent, so saving has to actually drop it —
  // along with the parameters, which belong to the pick that is gone.
  const snapshot = {
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
    image_pipeline_params: { capture_caption: true },
  }
  const built = buildImageProcessingConfig(snapshot, editsWith({ pipelineId: '', pipelineParams: {} }))

  assert.deepEqual(built, {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
  })
})

test('both dialogs drop an empty pick and empty parameters the same way', () => {
  // Regression: the upload dialog used to post `image_pipeline_params: {}`,
  // which reads as "the caller turned everything off" once a pipeline starts
  // reading its own keys with a default pinned at the read site.
  // A real pick with nothing to tune keeps the pick and drops the params;
  // an absent pick falls back to the backend's own resolution.
  assert.deepEqual(buildPipelineFields(IMAGE_PIPELINE_SMARTOCR, {}), {
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  })
  assert.deepEqual(buildPipelineFields('', { enable_ocr: true }), {
    image_pipeline_params: { enable_ocr: true },
  })
})

test('stored renames resolve to the ids the panel offers', () => {
  // Regression: `caption_ocr` predates the picker and `ob_cap_ocr` predates
  // the rename to match the label; a reparse of a document saved under either
  // spelling must land on a selectable option, not on an empty one.
  assert.equal(normalizeImagePipelineId(LEGACY_PIPELINE_CAPTION_OCR), 'default')
  assert.equal(normalizeImagePipelineId(LEGACY_PIPELINE_OB_CAP_OCR), IMAGE_PIPELINE_SMARTOCR)
  assert.equal(
    resolveImagePipelineFromKb({ image_processing_config: { image_pipeline: LEGACY_PIPELINE_OB_CAP_OCR } }),
    IMAGE_PIPELINE_SMARTOCR,
  )
  // Only the observation switch, no pick at all: the legacy rule still applies.
  assert.equal(
    resolveImagePipelineFromKb({ image_processing_config: { image_attrs_enabled: true } }),
    IMAGE_PIPELINE_SMARTOCR,
  )
  assert.equal(
    resolveImagePipelineFromKb({ image_processing_config: { image_attrs_enabled: false } }),
    'default',
  )
  // Nothing stored at all — the oldest knowledge bases.
  assert.equal(resolveImagePipelineFromKb({}), 'default')
})
