import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveInitialSidebarCollapsed } from './ui.ts'

test('first visit collapses the sidebar only on phone-sized viewports', () => {
  assert.equal(resolveInitialSidebarCollapsed(null, 360), true)
  assert.equal(resolveInitialSidebarCollapsed(null, 600), true)
  assert.equal(resolveInitialSidebarCollapsed(null, 601), false)
  assert.equal(resolveInitialSidebarCollapsed(null, 1440), false)
})

test('saved sidebar preference takes precedence over viewport width', () => {
  assert.equal(resolveInitialSidebarCollapsed('true', 1440), true)
  assert.equal(resolveInitialSidebarCollapsed('false', 360), false)
})
