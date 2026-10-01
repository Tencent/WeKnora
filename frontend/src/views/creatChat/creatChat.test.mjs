import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./creatChat.vue', import.meta.url), 'utf8')

test('new-session createSessions includes project_dir only through withOptionalProjectDir', () => {
  assert.match(source, /withOptionalProjectDir/)
  assert.match(source, /createSessions\(withOptionalProjectDir/)
  assert.match(source, /selectedProjectDir/)
  assert.match(source, /hostSandboxEnabled/)
  assert.doesNotMatch(source, /approvalMode|GetApprovalMode|SetApprovalMode/)
})

test('new-session page has a single project button flush above the composer', () => {
  assert.match(source, /pickHostProjectDir/)
  assert.match(source, /create-chat-composer/)
  assert.match(source, /createChat\.openProject/)
  assert.doesNotMatch(source, /wailsjs\/go\/main\/App/)
  assert.doesNotMatch(source, /PickProjectFile/)
  assert.doesNotMatch(source, /createChat\.openFile/)
  assert.doesNotMatch(source, /t-select/)
  assert.doesNotMatch(source, /project-dir-picker/)
})
