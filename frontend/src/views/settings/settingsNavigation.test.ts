import assert from 'node:assert/strict'
import test from 'node:test'

import { INTEGRATION_PREVIEW_ITEMS } from '../../config/integrations'
import { integrationSectionKey } from '../../config/settingsRoute'
import { BUILTIN_SETTINGS_GROUPS, BUILTIN_SETTINGS_SECTIONS } from '../../extensions/builtin/settingsCatalog'
import {
  groupSections,
  sectionPermitted,
  type SettingsAccessContext,
  type SettingsSection,
} from '../../extensions/settingsSections'

// The sidebar is built from the builtin catalog through the same grouping
// function Settings.vue uses.
const groups = BUILTIN_SETTINGS_GROUPS.map((g) => ({ key: g.key, order: g.order, label: () => g.label }))
const sections = BUILTIN_SETTINGS_SECTIONS.map((s) => ({ ...s, label: () => s.label }) as unknown as SettingsSection)

function menu(visibleKeys: string[], group: string): string[] {
  const visible = sections.filter((s) => visibleKeys.includes(s.key))
  return groupSections(groups, visible).find((g) => g.key === group)?.items.map((s) => s.key) ?? []
}

const integrationKeys = INTEGRATION_PREVIEW_ITEMS.map((item) => integrationSectionKey(item.key))

test('settings sidebar includes every registered integration, including CLI, in navigation order', () => {
  assert.deepEqual(menu(['general', ...integrationKeys], 'integrations'), integrationKeys)
})

test('settings sidebar preserves visibility filtering without hiding CLI', () => {
  const visible = integrationKeys.filter((key) => key !== 'integration-api' && key !== 'integration-im')
  assert.deepEqual(menu(visible, 'integrations'), visible)
  assert.deepEqual(menu(['general'], 'integrations'), [])
})

test('groups keep their order and drop when empty', () => {
  const all = sections.map((s) => s.key)
  assert.deepEqual(
    groupSections(groups, sections.filter((s) => all.includes(s.key))).map((g) => g.key),
    [
      'account', 'workspace', 'models_runtime', 'integrations', 'data_extensions', 'plugins',
      'system_administration', 'platform',
    ],
  )
  assert.deepEqual(groupSections(groups, sections.filter((s) => s.key === 'system')).map((g) => g.key), ['platform'])
  assert.deepEqual(menu(all, 'account'), ['general', 'userprofile', 'mymemory', 'envvars'])
  assert.deepEqual(menu(all, 'data_extensions'), ['vectorstore', 'parser', 'storage', 'sandbox', 'websearch'])
})

test('section keys are unique', () => {
  const keys = BUILTIN_SETTINGS_SECTIONS.map((s) => s.key)
  assert.equal(new Set(keys).size, keys.length)
})

test('access follows the shared role tables', () => {
  const ctx = (role: 'viewer' | 'admin', isSystemAdmin = false): SettingsAccessContext => {
    const rank = { viewer: 0, contributor: 1, admin: 2, owner: 3 }
    return {
      isSystemAdmin,
      canAccessAllTenants: false,
      hasRole: (min) => rank[role] >= rank[min],
      isSupported: () => true,
      capabilities: {},
    }
  }
  const byKey = new Map(sections.map((s) => [s.key, s]))
  assert.equal(sectionPermitted(byKey.get('models')!, ctx('viewer')), true)
  assert.equal(sectionPermitted(byKey.get('websearch')!, ctx('viewer')), false)
  assert.equal(sectionPermitted(byKey.get('websearch')!, ctx('admin')), true)
  assert.equal(sectionPermitted(byKey.get('integration-api')!, ctx('admin')), false, 'API integration is owner-only')
  assert.equal(sectionPermitted(byKey.get('runtime-queues')!, ctx('admin')), false)
  assert.equal(sectionPermitted(byKey.get('runtime-queues')!, ctx('viewer', true)), true)
})
