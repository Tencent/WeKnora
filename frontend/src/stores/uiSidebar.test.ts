import assert from 'node:assert/strict'
import test from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { useUIStore } from './ui.ts'

test('sidebar drag bounds, collapse, button restore, and saved width stay consistent', () => {
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const storage = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
    },
  })
  try {
    setActivePinia(createPinia())
    const ui = useUIStore()
    assert.equal(ui.sidebarDisplayWidth, 260)
    ui.resizeSidebar(900)
    assert.equal(ui.sidebarDisplayWidth, 280)
    ui.resizeSidebar(195)
    assert.equal(ui.sidebarDisplayWidth, 220)
    ui.resizeSidebar(270)
    ui.resizeSidebar(179)
    assert.equal(ui.sidebarCollapsed, true)
    assert.equal(ui.sidebarDisplayWidth, 60)
    ui.toggleSidebar()
    assert.equal(ui.sidebarDisplayWidth, 270, 'collapse keeps the last expanded width')
    ui.collapseSidebar()
    ui.resizeSidebar(240)
    assert.equal(ui.sidebarCollapsed, false, 'drag can reopen the collapsed sidebar')
    assert.equal(ui.sidebarDisplayWidth, 240)

    setActivePinia(createPinia())
    assert.equal(useUIStore().sidebarDisplayWidth, 240, 'reload restores the chosen width')
    storage.set('sidebar_width', '420')
    setActivePinia(createPinia())
    assert.equal(useUIStore().sidebarDisplayWidth, 280, 'old saved widths respect the reduced maximum')
    for (const invalid of ['NaN', '-1', '0', '']) {
      storage.set('sidebar_width', invalid)
      setActivePinia(createPinia())
      assert.equal(useUIStore().sidebarDisplayWidth, 260)
    }
  } finally {
    if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
    else Reflect.deleteProperty(globalThis, 'localStorage')
  }
})
