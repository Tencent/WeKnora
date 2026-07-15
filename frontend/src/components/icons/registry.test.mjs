import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const registry = readFileSync(join(here, 'registry.ts'), 'utf8')
const svgIcon = readFileSync(join(here, 'SvgIcon.vue'), 'utf8')
const menu = readFileSync(join(here, '../menu.vue'), 'utf8')
const agentStream = readFileSync(join(here, '../../views/chat/components/AgentStreamDisplay.vue'), 'utf8')
const deepThink = readFileSync(join(here, '../../views/chat/components/deepThink.vue'), 'utf8')

test('theme color map uses TDesign CSS variables with green brand fallback', () => {
  assert.match(registry, /brand:\s*'var\(--td-brand-color, #07c05f\)'/)
  assert.match(registry, /secondary:\s*'var\(--td-text-color-secondary/)
  assert.match(registry, /default:\s*'var\(--td-text-color-primary/)
})

test('registry glyphs paint via currentColor', () => {
  const contents = [...registry.matchAll(/content:\s*`([^`]+)`/g)].map((m) => m[1])
  assert.ok(contents.length >= 10)
  for (const content of contents) {
    assert.match(content, /currentColor/)
  }
})

test('SvgIcon resolves color from props.color, variant, then theme', () => {
  assert.match(svgIcon, /props\.color \?\?/)
  assert.match(svgIcon, /variantColorMap\[props\.variant\]/)
  assert.match(svgIcon, /themeColorMap\[props\.theme\]/)
})

test('menu switches brand/secondary via resolveMenuIcon theme', () => {
  assert.match(menu, /theme:\s*isActive \? 'brand' : 'secondary'/)
  assert.match(menu, /<SvgIcon/)
  assert.match(menu, /resolveMenuIcon\(item\)\.theme/)
})

test('agent stream titles use SvgIcon agent/thinking glyphs', () => {
  assert.match(agentStream, /SvgIcon name="agent"/)
  assert.match(agentStream, /SvgIcon name="thinking"/)
  assert.doesNotMatch(agentStream, /maskIconStyle/)
  assert.match(deepThink, /SvgIcon name="thinking"/)
})
