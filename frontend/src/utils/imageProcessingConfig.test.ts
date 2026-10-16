import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildImageProcessingConfig,
  buildPipelineFields,
  pipelineParamKey,
  pipelineParamsFromKb,
  readPipelineParam,
  resolveImagePipelineFromKb,
  normalizeImagePipelineId,
  IMAGE_PIPELINE_DEFAULT,
  IMAGE_PIPELINE_SMARTOCR,
  PREPROCESS_MODE_KEY,
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
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: true } },
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
  const built = buildImageProcessingConfig(snapshot, editsWith({
    pipelineId: '',
    pipelineParams: { enable_caption: false, enable_ocr: false },
  }))

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
  assert.deepEqual(buildPipelineFields('', { enable_ocr: true }), {})
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

test('a pipeline-private setting is stored under the slot of the pipeline that owns it', () => {
  // Regression: switching pipelines used to clear the parameters, so going
  // A -> B -> A lost what A was set to. Keys are namespaced per pipeline, so
  // both answers survive side by side and neither pipeline reads the other's.
  const params = {
    [pipelineParamKey(IMAGE_PIPELINE_DEFAULT, 'caption_enabled')]: false,
    [pipelineParamKey(IMAGE_PIPELINE_SMARTOCR, PREPROCESS_MODE_KEY)]: 'always',
  }
  assert.deepEqual(params, {
    'default.caption_enabled': false,
    'smartocr.preprocess.mode': 'always',
  })
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'), false)
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_SMARTOCR, PREPROCESS_MODE_KEY), 'always')
  // A key the other pipeline never set falls through to the field default
  // rather than to whatever the neighbouring slot happens to say.
  assert.equal(readPipelineParam(params, IMAGE_PIPELINE_DEFAULT, PREPROCESS_MODE_KEY), undefined)
  // A key that already carries this pipeline's namespace is not doubled.
  assert.equal(pipelineParamKey(IMAGE_PIPELINE_SMARTOCR, 'smartocr.preprocess.mode'), 'smartocr.preprocess.mode')
  // No pick at all, e.g. multimodal switched off: the bare key is left alone.
  assert.equal(pipelineParamKey('', 'caption_enabled'), 'caption_enabled')
})

test('the run reads a bare key written before the slots existed', () => {
  // Configs saved before keys were namespaced carry the bare spelling; the
  // slot wins once it exists, and the bare key is the fallback rather than a
  // second, competing source.
  assert.equal(readPipelineParam({ caption_enabled: false }, IMAGE_PIPELINE_DEFAULT, 'caption_enabled'), false)
  assert.equal(
    readPipelineParam(
      { caption_enabled: true, 'default.caption_enabled': false },
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

test('a knowledge base that stored a setting before the slots shows the value it runs with', () => {
  // Regression: the controls for these two are bound to the slot, so a base
  // whose value still lives in the older field would draw the declared default
  // and write it into the slot on the next save, shadowing the real setting.
  const params = pipelineParamsFromKb({
    image_processing_config: {
      image_pipeline: IMAGE_PIPELINE_SMARTOCR,
      preprocess_mode: 'off',
      image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
    },
  })
  assert.equal(params['smartocr.preprocess.mode'], 'off')
  assert.equal(params['smartocr.ocr_on_unobserved'], false)

  // A stored slot wins over the older field, which is exactly what the backend
  // resolves — the migration must not undo a value the operator already set.
  const alreadyMoved = pipelineParamsFromKb({
    image_processing_config: {
      image_pipeline: IMAGE_PIPELINE_SMARTOCR,
      preprocess_mode: 'off',
      image_pipeline_params: { 'smartocr.preprocess.mode': 'always' },
    },
  })
  assert.equal(alreadyMoved['smartocr.preprocess.mode'], 'always')

  // Nothing older to fold in: the parameters are handed through untouched, so
  // a knowledge base that never had these fields gains no keys.
  assert.deepEqual(
    pipelineParamsFromKb({ image_processing_config: { image_pipeline: IMAGE_PIPELINE_SMARTOCR } }),
    {},
  )
})
