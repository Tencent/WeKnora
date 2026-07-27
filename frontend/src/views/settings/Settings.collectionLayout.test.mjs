import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const settings = readFileSync(join(here, 'Settings.vue'), 'utf8')
const collection = readFileSync(join(here, 'AgentCollectionAdmin.vue'), 'utf8')

test('collection admin receives a dedicated wide settings layout', () => {
  assert.match(settings, /'settings-overlay--collection':\s*currentSection === 'agent-collection'/)
  assert.match(settings, /'settings-modal--collection':\s*currentSection === 'agent-collection'/)
  assert.match(settings, /\.settings-modal--collection\s*{[\s\S]*?max-width:\s*1320px/)
  assert.match(settings, /\.settings-modal--collection\s*{[\s\S]*?height:\s*min\(860px,\s*calc\(100vh\s*-\s*24px\)\)/)
})

test('collection content and detail drawer stay within narrow viewports', () => {
  assert.match(collection, /size="min\(840px, calc\(100vw - 16px\)\)"/)
  assert.match(collection, /class="mobile-profile-summary"/)
  assert.match(collection, /\.collection-table[^}]*nth-child\(4\)[\s\S]*display:\s*none;/)
  assert.match(settings, /@media\s*\(max-width:\s*800px\)[\s\S]*?\.settings-overlay--collection\s*{[\s\S]*?padding:\s*8px;/)
  assert.match(settings, /@media\s*\(max-width:\s*800px\)[\s\S]*?\.settings-modal--collection[\s\S]*?\.content-wrapper--full\s*{[\s\S]*?padding:\s*20px 18px 32px;/)
})

test('collection history reserves a dedicated column for timestamps', () => {
  assert.match(collection, /<t-timeline[^>]*class="collection-history"/)
  assert.match(collection, /\.collection-history[\s\S]*grid-template-columns:\s*152px 8px minmax\(0,\s*1fr\)/)
  assert.match(collection, /\.collection-history[\s\S]*\.t-timeline-item__label[\s\S]*position:\s*static/)
  assert.match(collection, /\.collection-history[\s\S]*\.t-timeline-item__wrapper[\s\S]*margin-left:\s*0\s*!important/)
})
