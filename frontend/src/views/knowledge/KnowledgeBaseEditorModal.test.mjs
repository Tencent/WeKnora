import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')

test('editing a knowledge base closes the editor after a successful save', () => {
  assert.match(source, /emit\('success', kbId\)\s*handleClose\(\)/)
})

test('the first successful create stays open for follow-up configuration', () => {
  const createBranch = source.match(
    /if \(editorMode\.value === 'create'\) \{([\s\S]*?)^\s{4}\} else \{/m
  )?.[1]

  assert.ok(createBranch, 'expected to find the create branch')
  assert.doesNotMatch(createBranch, /handleClose\(\)/)
  assert.match(createBranch, /savedKbId\.value = createdKbId/)
})

test('save button labels distinguish create from save-and-close', () => {
  assert.match(
    source,
    /const saveButtonLabel = computed\(\(\) =>\s*editorMode\.value === 'create'\s*\? t\('knowledgeEditor\.buttons\.create'\)\s*: t\('knowledgeEditor\.buttons\.saveAndClose'\)\s*\)/
  )
})

test('shows a post-create hint after the first successful save', () => {
  assert.match(source, /const isPostCreateSession = computed\(\(\) => !!savedKbId\.value\)/)
  assert.match(source, /settings-footer-note/)
  assert.match(source, /knowledgeEditor\.postCreateHint\.followUpDesc/)
})

test('create mode seeds the full default image actions table', () => {
  // Regression: an empty imageActions in initFormData made the attribute panel
  // read `undefined.ocr.on_unobserved` the moment the attribute switch was turned
  // on in the create dialog, crashing the modal.
  const initBlock = source.match(/const initFormData[\s\S]*?imageActions: ([^,]+),/)?.[1]
  assert.ok(initBlock, 'expected to find imageActions in initFormData')
  assert.match(initBlock, /mergeImageActions\(\)/)
})

test('edit mode forwards image_processing_config in the update payload', () => {
  // Regression: the edit branch built data.image_processing_config but never
  // put it into updateConfig, so KB-editor changes to the image classification
  // settings were silently dropped (create worked, later edits did not).
  const editBranch = source.match(
    /(\/\/ 编辑模式：分别更新基本信息[\s\S]*?await updateKnowledgeBase\(kbId, \{)/
  )?.[1]
  assert.ok(editBranch, 'expected to find the edit-mode update block')
  assert.match(editBranch, /if \(data\.image_processing_config\) \{\s*updateConfig\.image_processing_config = data\.image_processing_config\s*\}/)
})

test('video parsing saves its own ASR language and never borrows the audio model', () => {
  // The video section is independent: emptying its model must not resurrect the
  // audio section's model, and its language hint has to round-trip.
  const payload = source.match(/data\.video_config = \{([\s\S]*?)\n  \}/)?.[1]
  assert.ok(payload, 'expected to find the video_config payload block')
  assert.match(payload, /language: formData\.value\.videoConfig\?\.language \|\| ''/)

  const configured = source.match(
    /const videoAudioModelConfigured = computed\(\(\) =>\s*([\s\S]*?)\)\n/
  )?.[1]
  assert.ok(configured, 'expected to find videoAudioModelConfigured')
  assert.match(configured, /isVideoAudioModelConfigured\(formData\.value\?\.videoConfig\)/)
  assert.doesNotMatch(configured, /asrConfig/)
})

test('video frame descriptions carry their own vlm_config, not the image section', () => {
  // The video page owns its vision model and prompt: saving the editor must
  // send video_config.vlm_config, and the warning state must read the video
  // section instead of multimodalConfig.
  const payload = source.match(/data\.video_config = \{([\s\S]*?)\n  \}/)?.[1]
  assert.ok(payload, 'expected to find the video_config payload block')

  const vlmPayload = payload.match(/vlm_config: \{([\s\S]*?)\n    \}/)?.[1]
  assert.ok(vlmPayload, 'expected to find the nested vlm_config payload')
  assert.match(vlmPayload, /enabled: !!videoVLMModelId/)
  assert.match(vlmPayload, /model_id: videoVLMModelId/)
  assert.match(vlmPayload, /description_language: videoVLM\?\.descriptionLanguage \|\| ''/)
  assert.match(vlmPayload, /custom_instructions: videoVLM\?\.customInstructions \|\| ''/)

  const configured = source.match(
    /const videoFrameModelConfigured = computed\(\(\) =>\s*([\s\S]*?)\)\n/
  )?.[1]
  assert.ok(configured, 'expected to find videoFrameModelConfigured')
  assert.match(configured, /isVideoFrameModelConfigured\(formData\.value\?\.videoConfig\)/)
  assert.doesNotMatch(configured, /multimodalConfig/)
})

test('video frame settings round-trip from the stored video_config.vlm_config', () => {
  // The edit-mode initializer is the block that reads the stored KB.
  const editInit = source.match(
    /modelId: kb\.video_config\?\.model_id \|\| '',[\s\S]*?vlm: \{([\s\S]*?)\n\s*\}\n\s*\},/
  )?.[1]
  assert.ok(editInit, 'expected to find the stored video vlm initializer')
  assert.match(editInit, /modelId: kb\.video_config\?\.vlm_config\?\.model_id \|\| ''/)
  assert.match(editInit, /descriptionLanguage: kb\.video_config\?\.vlm_config\?\.description_language \|\| ''/)
  assert.match(editInit, /customInstructions: kb\.video_config\?\.vlm_config\?\.custom_instructions \|\| ''/)
})
