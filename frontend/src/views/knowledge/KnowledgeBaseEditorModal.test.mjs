import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')
const graphSettingsSource = readFileSync(new URL('./settings/GraphSettings.vue', import.meta.url), 'utf8')

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

test('blocks incomplete graph extraction config and opens graph settings', () => {
  const validationBlock = source.match(
    /if \(formData\.value\.type !== 'faq' && !isGraphExtractConfigComplete\(formData\.value\.nodeExtractConfig\)\) \{([\s\S]*?)^\s{2}\}/m
  )?.[1]

  assert.ok(validationBlock, 'expected to find the graph extraction validation block')
  assert.match(validationBlock, /MessagePlugin\.warning\(t\('graphSettings\.completeConfigRequired'\)\)/)
  assert.match(validationBlock, /currentSection\.value = 'graph'/)
  assert.match(validationBlock, /return false/)
  assert.match(source, /if \(!validateForm\(\)\) \{\s*return\s*\}/)
})

test('never enables graph extraction in FAQ payloads', () => {
  assert.match(
    source,
    /enabled: formData\.value\.type !== 'faq' && !!formData\.value\.nodeExtractConfig\.enabled/
  )
})

test('graph required fields use the same asterisk marker as model settings', () => {
  assert.equal(
    graphSettingsSource.match(/<span class="required">\*<\/span>/g)?.length,
    4
  )
  assert.doesNotMatch(graphSettingsSource, /graphSettings\.required/)
})
