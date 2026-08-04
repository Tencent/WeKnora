import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const tree = readFileSync(new URL('./KbFolderTree.vue', import.meta.url), 'utf8')
const batchBar = readFileSync(new URL('./DocumentBatchBar.vue', import.meta.url), 'utf8')
const cardView = readFileSync(new URL('./DocumentCardView.vue', import.meta.url), 'utf8')
const listView = readFileSync(new URL('./DocumentListView.vue', import.meta.url), 'utf8')

// The root folder's own path IS the empty string, so a falsy "nothing is being
// renamed" sentinel matches it and leaves a stray rename input on the root row.
// Two independent guards keep that from coming back.
test('the rename sentinel cannot collide with the root folder path', () => {
  assert.match(tree, /const renamingPath = ref<string \| null>\(null\)/)
  assert.doesNotMatch(tree, /const renamingPath = ref\(''\)/)
  assert.match(tree, /row\.kind === 'folder' && renamingPath\.value === row\.path/)
})

test('only real folders expose a rename affordance', () => {
  assert.match(tree, /v-if="canEdit && row\.kind === 'folder'"/)
})

// Picking a folder is a small, reversible action, so it stays a popup: in the row
// menu it is another level of the menu that is already open, and in the batch bar
// it hangs off the button. Neither should escalate to a modal dialog.
test('the folder picker is a popup rather than a modal', () => {
  assert.match(cardView, /folderPickerItemId === item\.id/)
  assert.match(listView, /folderPickerItemId === item\.id/)
  assert.match(batchBar, /<t-popup[^>]*v-model:visible="folderPickerVisible"/)
  for (const source of [cardView, listView, batchBar]) {
    assert.doesNotMatch(source, /MoveToFolderDialog/)
  }
})
