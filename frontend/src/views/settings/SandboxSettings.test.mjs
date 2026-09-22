import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./SandboxSettings.vue', import.meta.url), 'utf8')

test('sandbox settings do not manage host project directories', () => {
  assert.doesNotMatch(source, /PickProjectDir/)
  assert.doesNotMatch(source, /PickProjectFile/)
  assert.doesNotMatch(source, /GetProjectDirs/)
  assert.doesNotMatch(source, /SetProjectDirs/)
  assert.doesNotMatch(source, /host-project-dirs/)
  assert.doesNotMatch(source, /hostProjectsTitle/)
  assert.doesNotMatch(source, /shouldRenderHostProjectSettings/)
  assert.match(source, /class="sandbox-settings"/)
})
