import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./SettingDrawer.vue', import.meta.url), 'utf8')

test('the title row hosts compact header actions without replacing the step rail', () => {
  assert.match(source, /headerActionsId/)
  assert.match(source, /SETTING_DRAWER_HEADER_ACTIONS_ID/)
  assert.match(source, /header-actions/)
  assert.match(source, /header-extra/)
  assert.match(source, /setting-drawer__header-actions/)
  assert.match(source, /&:empty/)
})
