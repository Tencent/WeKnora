import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./ChatHeader.vue', import.meta.url), 'utf8')

test('session header shows basename or temporary workspace, never the absolute path', () => {
  assert.match(source, /hostWorkspaceHeaderText/)
  assert.match(source, /chatHeader\.temporaryWorkspace/)
  assert.match(source, /workspaceLabel/)
  assert.doesNotMatch(source, /session\?\.host_workspace_dir\b/)
  assert.doesNotMatch(source, /\{\{\s*session\.host_workspace_dir/)
})
