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
