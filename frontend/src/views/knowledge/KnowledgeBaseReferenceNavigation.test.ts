import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')

test('referenced documents navigate to their containing folder before opening details', () => {
  const resolveFolder = source.indexOf('await getKnowledgeDetails(targetId)')
  const selectFolder = source.indexOf('selectedFolderPath.value = detail.folder_path || ROOT_FOLDER_PATH')
  const openDetails = source.indexOf('openCardDetails(target)', selectFolder)

  assert.ok(resolveFolder >= 0, 'document details should be fetched to resolve folder_path')
  assert.ok(selectFolder > resolveFolder, 'the containing folder should be selected after details load')
  assert.ok(openDetails > selectFolder, 'the detail drawer should open after folder navigation')
})

test('a newer referenced-document navigation supersedes an older request', () => {
  assert.match(source, /const request = \+\+autoOpenRequest/)
  assert.match(source, /if \(request !== autoOpenRequest\) return;/)
})
