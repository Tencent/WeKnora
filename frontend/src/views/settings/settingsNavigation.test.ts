import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { computed } from 'vue'
import { INTEGRATION_PREVIEW_ITEMS } from '../../config/integrations'
import { integrationSectionKey } from '../../config/settingsRoute'

// Exercise the actual sidebar grouping without mounting the settings panels.
const source = readFileSync(new URL('./Settings.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)![1]!
const ast = ts.createSourceFile('Settings.ts', script, ts.ScriptTarget.Latest, true)
const grouping = ast.statements.find((statement) =>
  ts.isVariableStatement(statement) && statement.declarationList.declarations.some(
    (declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === 'navGroups',
  ),
)
assert.ok(grouping, 'Settings must define sidebar groups')
const compiled = ts.transpileModule(grouping.getText(ast), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

function integrationMenu(visibleKeys: string[]): string[] {
  const groups = runInNewContext(`${compiled}\nnavGroups.value`, {
    computed,
    navItems: { value: visibleKeys.map((key) => ({ key })) },
    t: (key: string) => key,
    integrationSectionKey,
    INTEGRATION_PREVIEW_ITEMS,
  }) as Array<{ key: string; items: Array<{ key: string }> }>
  const integrationGroup = groups.find((group) => group.key === 'integrations')
  return Array.from(integrationGroup?.items ?? [], (item) => item.key)
}

const integrationKeys = INTEGRATION_PREVIEW_ITEMS.map((item) => integrationSectionKey(item.key))

test('settings sidebar includes every registered integration, including CLI, in navigation order', () => {
  assert.deepEqual(integrationMenu(['general', ...integrationKeys]), integrationKeys)
})

test('settings sidebar preserves visibility filtering without hiding CLI', () => {
  const visible = integrationKeys.filter((key) => key !== 'integration-api' && key !== 'integration-im')
  assert.deepEqual(integrationMenu(visible), visible)
  assert.deepEqual(integrationMenu(['general']), [])
})
