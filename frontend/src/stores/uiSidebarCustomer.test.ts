import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { useUIStore } from './ui.ts'

/**
 * 客户版侧栏状态（AC-29 / C6 / M4 / M5 / M6）。
 *
 * 客户收起/展开只改内存态，绝不能覆盖同一浏览器里管理员的 sidebar_collapsed；
 * 切换历史会话时客户侧栏默认展开；收起后侧栏 inert，键盘进不去。
 */

const topBar = readFileSync(new URL('../components/customer/CustomerTopBar.vue', import.meta.url), 'utf8')
const menuVue = readFileSync(new URL('../components/menu.vue', import.meta.url), 'utf8')
const chatView = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')

interface StorageHarness {
  data: Map<string, string>
  writes: Array<{ key: string; value: string }>
  restore: () => void
}

function installStorage(initial: Record<string, string> = {}): StorageHarness {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const data = new Map(Object.entries(initial))
  const writes: Array<{ key: string; value: string }> = []
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => data.get(key) ?? null,
      setItem: (key: string, value: string) => {
        writes.push({ key, value })
        data.set(key, value)
      },
      removeItem: (key: string) => {
        data.delete(key)
      },
    },
  })
  return {
    data,
    writes,
    restore: () => {
      if (original) Object.defineProperty(globalThis, 'localStorage', original)
      else Reflect.deleteProperty(globalThis, 'localStorage')
    },
  }
}

test('persist:false changes memory only and never overwrites the admin sidebar preference', () => {
  const storage = installStorage({ sidebar_collapsed: 'true' })
  try {
    setActivePinia(createPinia())
    const ui = useUIStore()
    assert.equal(ui.sidebarCollapsed, true, 'the admin preference is loaded')

    ui.toggleSidebar({ persist: false })
    assert.equal(ui.sidebarCollapsed, false)
    assert.equal(storage.data.get('sidebar_collapsed'), 'true')
    assert.deepEqual(storage.writes, [])

    ui.collapseSidebar({ persist: false })
    assert.equal(ui.sidebarCollapsed, true)
    assert.deepEqual(storage.writes, [])

    ui.expandSidebar({ persist: false })
    assert.equal(ui.sidebarCollapsed, false)
    assert.deepEqual(storage.writes, [])

    // 管理员路径（不传 persist）仍然持久化，行为与改造前一致。
    ui.collapseSidebar()
    assert.deepEqual(storage.writes, [{ key: 'sidebar_collapsed', value: 'true' }])
    assert.equal(storage.data.get('sidebar_collapsed'), 'true')
  } finally {
    storage.restore()
  }
})

test('customer sidebar entry points route through persist:false; admin keeps persistence', () => {
  // U4 后顶栏不再放开关：客户收起入口 = 侧栏 logo 行折叠按钮（persist 取 !isCustomer），
  // 收起态的展开入口 = 侧栏浮出栏按钮（persist:false，仅客户渲染，C6）。
  assert.doesNotMatch(topBar, /toggleSidebar|collapseSidebar/)
  // 刷新默认展开（AC-29）仍由顶栏 onMounted 重置，只改内存态（M4）。
  assert.match(topBar, /uiStore\.expandSidebar\(\{ persist: false \}\)/)
  assert.match(menuVue, /uiStore\.toggleSidebar\(\{ persist: !isCustomer\.value \}\)/)
  assert.match(menuVue, /const persist = !isCustomer\.value/)
  assert.match(menuVue, /uiStore\.expandSidebar\(\{ persist \}\)/)
  assert.match(menuVue, /uiStore\.collapseSidebar\(\{ persist \}\)/)
  // 浮出栏：仅客户渲染，点击 expandSidebar({persist:false})，展开态下自身 inert。
  assert.match(menuVue, /<div v-if="isCustomer" class="sidebar-float-rail"/)
  assert.match(menuVue, /@click="uiStore\.expandSidebar\(\{ persist: false \}\)"/)
  assert.match(menuVue, /:inert="!uiStore\.sidebarCollapsed"/)
})

test('switching between customer sessions expands the sidebar without persisting (M5)', () => {
  const routeWatcher = chatView.slice(
    chatView.indexOf('watch([() => route.params]'),
    chatView.indexOf('const scrollFollow'),
  )
  assert.ok(routeWatcher.length > 0, 'chat view must keep the route.params watcher')
  assert.match(routeWatcher, /if \(isCustomer\.value && uiStore\.sidebarCollapsed\)/)
  assert.match(routeWatcher, /uiStore\.expandSidebar\(\{ persist: false \}\)/)
})

test('collapsed customer sidebar is inert and hidden from assistive tech (M6)', () => {
  assert.match(menuVue, /:inert="isCustomer && uiStore\.sidebarCollapsed"/)
  assert.match(menuVue, /:aria-hidden="isCustomer && uiStore\.sidebarCollapsed \? 'true' : undefined"/)
  // 过渡保留：宽度动画仍挂在客户专用类上。
  assert.match(menuVue, /aside_box--customer-hidden/)
})
