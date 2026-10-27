import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildImageProcessingConfig,
  buildPipelineFields,
  pipelineParamKey,
  readPipelineParam,
  resolveImagePipelineFromKb,
  normalizeImagePipelineId,
  IMAGE_PIPELINE_DEFAULT,
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
    imageVectorEnabled: false,
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
    { model_id: 'vlm-1', image_attrs_enabled: true, image_actions: { ocr: { on: custom } } },
    editsWith(),
  )

  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: custom } },
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  })
})

test('a KB without custom conditions gets the registry default', () => {
  const built = buildImageProcessingConfig({ model_id: 'vlm-1' }, editsWith())

  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON } },
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  })
})

test('an older on_unobserved in the table is dropped rather than carried forward', () => {
  // The clause moved out of the attribute table: what to do about an image whose
  // attributes were not observed is a setting of the pipeline that schedules
  // OCR. Keeping the old key in the stored table would leave two places claiming
  // to answer for one decision, so saving drops it.
  const built = buildImageProcessingConfig(
    { image_attrs_enabled: true, image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } } },
    editsWith(),
  )

  assert.deepEqual(built?.image_actions, { ocr: { on: DEFAULT_ON } })
})

test('the chosen pipeline and every setting of it are written back', () => {
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    editsWith({
      pipelineId: IMAGE_PIPELINE_DEFAULT,
      pipelineParams: { 'default.caption_enabled': false, 'default.ocr_enabled': true },
    }),
  )

  // The map travels whole, in the stored shape — every key under the slot of the
  // pipeline that declared it. A misspelled key is silently ignored at run time,
  // so this passthrough is deliberately dumb: the panel never re-keys anything.
  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON } },
    image_pipeline: IMAGE_PIPELINE_DEFAULT,
    image_pipeline_params: { 'default.caption_enabled': false, 'default.ocr_enabled': true },
  })
})

test('an unchanged configuration is not sent', () => {
  const custom = [{ prop: 'contain.text', is: 'sparse' }]
  const snapshot = {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: custom } },
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  }

  assert.equal(buildImageProcessingConfig(snapshot, editsWith()), null)
})

test('the image-vector switch is saved when turned on and when turned back off', () => {
  const on = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    editsWith({ imageAttrsEnabled: false, imageVectorEnabled: true }),
  )
  assert.equal(on?.image_vector_enabled, true)

  const off = buildImageProcessingConfig(
    { model_id: 'vlm-1', image_vector_enabled: true },
    editsWith({ imageAttrsEnabled: false, imageVectorEnabled: false }),
  )
  assert.equal(off?.image_vector_enabled, false)
})

test('a knowledge base that never touched the image-vector switch does not get one written', () => {
  // The backend reads a missing key as off, which is what every knowledge
  // base from before the switch has; an unrelated save must leave it so.
  const snapshot = {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON } },
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
  }
  assert.equal(buildImageProcessingConfig(snapshot, editsWith()), null)
})

test('clearing the pick removes the stored one rather than repeating it', () => {
  // An empty pick means "resolve from image_attrs_enabled", which is what the
  // field already said by being absent, so saving has to actually drop it —
  // along with the parameters, which belong to the pick that is gone.
  const snapshot = {
    image_pipeline: IMAGE_PIPELINE_SMARTOCR,
    image_pipeline_params: { 'smartocr.ocr_on_unobserved': false },
  }
  const built = buildImageProcessingConfig(
    snapshot,
    editsWith({
      pipelineId: '',
      pipelineParams: { 'default.caption_enabled': false, 'default.ocr_enabled': false },
    }),
  )

  assert.deepEqual(built, {
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON } },
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
  assert.deepEqual(buildPipelineFields('', { 'default.ocr_enabled': true }), {})
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

test('a pipeline setting is stored under the slot of the pipeline that owns it', () => {
  // Regression: switching pipelines used to clear the parameters, so going
  // A -> B -> A lost what A was set to. Keys are namespaced per pipeline, so
  // both answers survive side by side and neither pipeline reads the other's.
  const params = {
    [pipelineParamKey(IMAGE_PIPELINE_DEFAULT, 'caption_enabled')]: false,
    [pipelineParamKey(IMAGE_PIPELINE_SMARTOCR, 'preprocess.mode')]: 'always',
  }
  assert.deepEqual(params, {
    'default.caption_enabled': false,
    'smartocr.preprocess.mode': 'always',
  })
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'), false)
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_SMARTOCR, 'preprocess.mode'), 'always')
  // A key the other pipeline never set reads as not stored, which is what makes
  // the panel draw the declared default rather than the neighbouring slot.
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_DEFAULT, 'preprocess.mode'), undefined)
  // A key that already carries this pipeline's namespace is not doubled.
  assert.equal(pipelineParamKey(IMAGE_PIPELINE_SMARTOCR, 'smartocr.preprocess.mode'), 'smartocr.preprocess.mode')
  // No pick at all, e.g. multimodal switched off: the bare key is left alone.
  assert.equal(pipelineParamKey('', 'caption_enabled'), 'caption_enabled')
})

test('the panel reads the slot and nowhere else', () => {
  // The slot is the only home a setting has, so there is no fallback spelling to
  // resolve: a key stored without one is not read, and "not stored" is what
  // makes the panel and the run agree on the declared default.
  assert.equal(readPipelineParam({ caption_enabled: false }, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'), undefined)
  assert.equal(
    readPipelineParam(
      { 'default.caption_enabled': false, 'default.ocr_enabled': null },
      IMAGE_PIPELINE_DEFAULT,
      'caption_enabled',
    ),
    false,
  )
  assert.equal(readPipelineParam({}, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'), undefined)
  assert.equal(readPipelineParam(null, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'), undefined)
  // An explicitly null slot is "nothing stored", not "stored as null".
  assert.equal(
    readPipelineParam({ 'default.caption_enabled': null }, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'),
    undefined,
  )
})

test('what the panel draws for a knowledge base is what the run reads', () => {
  // Regression: the controls are bound to the slot, so a base whose value lived
  // in an older field used to draw the declared default instead of the value it
  // runs with. With one home per setting, both sides read the same map.
  const kb = {
    image_processing_config: {
      image_pipeline: IMAGE_PIPELINE_SMARTOCR,
      image_pipeline_params: {
        'smartocr.preprocess.mode': 'off',
        'smartocr.ocr_on_unobserved': false,
      },
    },
  }
  const params = kb.image_processing_config.image_pipeline_params

  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_SMARTOCR, 'preprocess.mode'), 'off')
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_SMARTOCR, 'ocr_on_unobserved'), false)
  // A field the slot does not carry is not stored, and the panel draws the
  // default the field declares — exactly the value the run would use.
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_SMARTOCR, 'thinking_enabled'), undefined)
})
