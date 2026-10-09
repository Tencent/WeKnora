import assert from 'node:assert/strict'
import test from 'node:test'

import { buildImageProcessingConfig } from './imageProcessingConfig.ts'

const DEFAULT_ON = [
  { prop: 'contain.text', is: 'block' },
  { prop: 'contain.data_visual', is: 'true' },
]

test('saving keeps OCR conditions customised through the API', () => {
  // Regression: the editor always wrote the registry default into `on`, so an
  // unrelated edit replaced a KB's custom OCR conditions with the default table.
  const custom = [{ prop: 'contain.text', is: 'sparse' }]
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1', image_attrs_enabled: true, image_actions: { ocr: { on: custom, on_unobserved: true } } },
    { imageAttrsEnabled: true, imageVectorEnabled: false, onUnobserved: false, defaultOn: DEFAULT_ON },
  )

  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: custom, on_unobserved: false } },
  })
})

test('a KB without custom conditions gets the registry default alongside on_unobserved', () => {
  const built = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    { imageAttrsEnabled: true, imageVectorEnabled: false, onUnobserved: false, defaultOn: DEFAULT_ON },
  )

  assert.deepEqual(built, {
    model_id: 'vlm-1',
    image_attrs_enabled: true,
    image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: false } },
  })
})

test('an unchanged configuration is not sent', () => {
  const custom = [{ prop: 'contain.text', is: 'sparse' }]
  const snapshot = { image_attrs_enabled: true, image_actions: { ocr: { on: custom, on_unobserved: true } } }

  assert.equal(
    buildImageProcessingConfig(snapshot, { imageAttrsEnabled: true, imageVectorEnabled: false, onUnobserved: true, defaultOn: DEFAULT_ON }),
    null,
  )
})

test('the image-vector switch is saved when turned on and when turned back off', () => {
  const on = buildImageProcessingConfig(
    { model_id: 'vlm-1' },
    { imageAttrsEnabled: false, imageVectorEnabled: true, onUnobserved: true, defaultOn: DEFAULT_ON },
  )
  assert.equal(on?.image_vector_enabled, true)

  const off = buildImageProcessingConfig(
    { model_id: 'vlm-1', image_vector_enabled: true },
    { imageAttrsEnabled: false, imageVectorEnabled: false, onUnobserved: true, defaultOn: DEFAULT_ON },
  )
  assert.equal(off?.image_vector_enabled, false)
})

test('a knowledge base that never touched the image-vector switch does not get one written', () => {
  // The backend reads a missing key as off, which is what every knowledge
  // base from before the switch has; an unrelated save must leave it so.
  const snapshot = { image_attrs_enabled: true, image_actions: { ocr: { on: DEFAULT_ON, on_unobserved: true } } }
  assert.equal(
    buildImageProcessingConfig(snapshot, {
      imageAttrsEnabled: true, imageVectorEnabled: false, onUnobserved: true, defaultOn: DEFAULT_ON,
    }),
    null,
  )
})
