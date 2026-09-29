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
    { imageAttrsEnabled: true, onUnobserved: false, defaultOn: DEFAULT_ON },
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
    { imageAttrsEnabled: true, onUnobserved: false, defaultOn: DEFAULT_ON },
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
    buildImageProcessingConfig(snapshot, { imageAttrsEnabled: true, onUnobserved: true, defaultOn: DEFAULT_ON }),
    null,
  )
})
