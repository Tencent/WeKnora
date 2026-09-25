import assert from 'node:assert/strict'
import test from 'node:test'

import {
  hostWorkspaceHeaderText,
  projectDirBasename,
  shouldRenderHostProjectSettings,
  withOptionalProjectDir,
} from './hostWorkspace.ts'

test('host-capability off hides the new-session project picker', () => {
  assert.equal(shouldRenderHostProjectSettings(false), false)
})

test('host-capability on shows the new-session project picker', () => {
  assert.equal(shouldRenderHostProjectSettings(true), true)
})

test('createSession payload includes project_dir when a project is selected', () => {
  const body = withOptionalProjectDir({ agent_config: { enabled: true } }, '/Users/dev/My Project')
  assert.equal(body.project_dir, '/Users/dev/My Project')
  assert.equal(body.agent_config.enabled, true)
})

test('createSession payload omits project_dir when unbound', () => {
  const empty = withOptionalProjectDir({ agent_config: { enabled: true } }, '')
  assert.equal(Object.hasOwn(empty, 'project_dir'), false)

  const missing = withOptionalProjectDir({ agent_config: { enabled: true } })
  assert.equal(Object.hasOwn(missing, 'project_dir'), false)

  const whitespace = withOptionalProjectDir({ agent_config: { enabled: true } }, '   ')
  assert.equal(Object.hasOwn(whitespace, 'project_dir'), false)

  const stripped = withOptionalProjectDir(
    { agent_config: { enabled: true }, project_dir: '' },
    '',
  )
  assert.equal(Object.hasOwn(stripped, 'project_dir'), false)
})

test('session header shows the project basename, not the absolute path', () => {
  assert.equal(projectDirBasename('/Users/dev/My Project'), 'My Project')
  assert.equal(projectDirBasename('C:\\Users\\dev\\My Project'), 'My Project')
  assert.equal(
    hostWorkspaceHeaderText('/Users/dev/My Project', 'Temporary workspace'),
    'My Project',
  )
  const label = hostWorkspaceHeaderText('/Users/dev/My Project', '临时工作区')
  assert.equal(label.includes('/'), false)
  assert.equal(label.includes('\\'), false)
  assert.notEqual(label, '/Users/dev/My Project')
})

test('session header shows the temporary workspace label when unbound', () => {
  assert.equal(hostWorkspaceHeaderText('', '临时工作区'), '临时工作区')
  assert.equal(hostWorkspaceHeaderText(undefined, 'Temporary workspace'), 'Temporary workspace')
})
