import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { computed, ref } from 'vue'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { TOOLBOX_ITEMS, canAccessToolboxSection, isToolboxSection, toolboxLocation } from './toolbox'
import zhCN from '../i18n/locales/zh-CN'

const roles = ['viewer', 'contributor', 'admin', 'owner']
const access = (role: string | null, unsupported: string[] = [], superuser = false) => ({
  currentTenantRole: role,
  canAccessAllTenants: superuser,
  hasRole: (min: string) => roles.indexOf(role || '') >= roles.indexOf(min),
  isSupported: (capability?: string) => !capability || !unsupported.includes(capability),
})

test('toolbox contains only the agreed tools; infrastructure and secrets stay in settings', () => {
  assert.deepEqual(TOOLBOX_ITEMS.map((item) => item.key), ['skills', 'mcp', 'browserconnection'])
  const lookup = (key: string) => key.split('.').reduce<any>((node, part) => node?.[part], zhCN)
  for (const item of TOOLBOX_ITEMS) {
    for (const key of [item.title, item.description, 'help' in item ? item.help : undefined,
      'action' in item ? item.action : undefined]) {
      if (key) assert.equal(typeof lookup(key), 'string', key)
    }
  }
  for (const key of ['sandbox', 'websearch', 'envvars', 'integration-api', undefined, 'unknown']) {
    assert.equal(isToolboxSection(key), false)
  }
})

test('toolbox preserves role and deployment gates, including workspace switches', () => {
  for (const role of ['viewer', 'contributor']) {
    assert.equal(canAccessToolboxSection('skills', access(role)), false)
    assert.equal(canAccessToolboxSection('mcp', access(role)), false)
    assert.equal(canAccessToolboxSection('browserconnection', access(role)), true)
  }
  assert.equal(canAccessToolboxSection('skills', access('admin')), true)
  assert.equal(canAccessToolboxSection('skills', access('owner', ['settings.sandbox'])), false)
  assert.equal(canAccessToolboxSection('mcp', access('owner', ['settings.mcp'])), false)
  assert.equal(canAccessToolboxSection('mcp', access(null)), false)
  assert.equal(canAccessToolboxSection('mcp', access(null, [], true)), true)
  assert.equal(canAccessToolboxSection('mcp', access(null, ['settings.mcp'], true)), false)
  const role = ref('admin')
  const allowed = computed(() => canAccessToolboxSection('skills', access(role.value)))
  assert.equal(allowed.value, true)
  role.value = 'viewer'
  assert.equal(allowed.value, false)
})

test('skill shortcut retains its target sandbox without leaking it to other tools', () => {
  assert.deepEqual(toolboxLocation('skills', 'sandbox-123'), {
    path: '/platform/toolbox/skills', query: { sandboxId: 'sandbox-123' },
  })
  assert.deepEqual(toolboxLocation('browserconnection', 'sandbox-123'), {
    path: '/platform/toolbox/browserconnection', query: {},
  })
  assert.deepEqual(toolboxLocation(), { path: '/platform/toolbox', query: {} })
})

const settings = readFileSync(new URL('../views/settings/Settings.vue', import.meta.url), 'utf8')
const script = settings.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)![1]!
const ast = ts.createSourceFile('Settings.ts', script, ts.ScriptTarget.Latest, true)
function declaration(name: string) {
  const node = ast.statements.find((s) => ts.isVariableStatement(s)
    && s.declarationList.declarations.some((d) => ts.isIdentifier(d.name) && d.name.text === name))
  assert.ok(node, `${name} exists`)
  return ts.transpileModule(node.getText(ast), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
}

test('legacy shortcuts close settings and route to the selected tool and sandbox', () => {
  const calls: unknown[] = []
  const redirect = runInNewContext(`${declaration('redirectToToolbox')}\nredirectToToolbox`, {
    isToolboxSection, toolboxLocation,
    uiStore: { closeSettings: () => calls.push('close') },
    router: { push: (location: unknown) => calls.push(location) },
  })
  assert.equal(redirect('skills', 'sandbox-123'), true)
  assert.deepEqual(calls, ['close', toolboxLocation('skills', 'sandbox-123')])
  calls.length = 0
  assert.equal(redirect('sandbox'), false)
  assert.deepEqual(calls, [])
  assert.match(script, /redirectToToolbox\(normalizedSection, uiStore.settingsInitialSubSection\)/)
  assert.match(script, /redirectToToolbox\(normalizedSection, subsection\)/)
})

test('settings no longer render moved panels, and old bookmarks reach toolbox', () => {
  for (const component of ['SkillSettings', 'McpSettings', 'BrowserConnectionSettings']) {
    assert.equal(settings.includes(`<${component}`), false)
  }
  for (const item of TOOLBOX_ITEMS) {
    assert.equal(settings.includes(`{ key: '${item.key}',`), false)
  }
  const router = readFileSync(new URL('../router/index.ts', import.meta.url), 'utf8')
  assert.match(router, /to\.path === '\/platform\/settings' && isToolboxSection\(to\.query\.section\)/)
  assert.match(router, /toolboxLocation\(to\.query\.section/)
  const page = readFileSync(new URL('../views/toolbox/Toolbox.vue', import.meta.url), 'utf8')
  assert.match(page, /if \(!selectedItem\.value && fallback\) void router\.replace\(toolboxLocation\(fallback\.key\)\)/)
  assert.match(page, /:key="sandboxId"\s+:initial-sandbox-id="sandboxId"/)
})
