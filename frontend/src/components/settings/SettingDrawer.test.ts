import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const sourceDir = dirname(fileURLToPath(import.meta.url))
const drawerSource = readFileSync(resolve(sourceDir, 'SettingDrawer.vue'), 'utf8')

test('设置抽屉保留可访问的显式关闭入口', () => {
  assert.match(drawerSource, /class="setting-drawer__close"/)
  assert.match(drawerSource, /:aria-label="t\('common\.close'\)"/)
  assert.match(drawerSource, /:disabled="cancelDisabled"/)
  assert.match(drawerSource, /@click="handleCancel"/)
})

test('设置抽屉在常见移动端宽度保持紧凑单行页脚', () => {
  assert.match(drawerSource, /@media \(max-width: 720px\)[\s\S]*setting-drawer-resize-handle/)
  assert.match(drawerSource, /\.setting-drawer__footer \{[\s\S]*flex-wrap: wrap/)
  assert.match(drawerSource, /\.setting-drawer__footer-right \{[\s\S]*flex-wrap: wrap/)
  assert.doesNotMatch(drawerSource, /flex:\s*1 1 100%/)
})

test('设置抽屉在视口钳制宽度后保持拖拽手柄贴边并完整显示副标题', () => {
  assert.match(drawerSource, /Math\.min\(drawerWidthPx\.value, viewportWidthPx\.value\)/)
  assert.match(drawerSource, /Math\.max\(0, visibleDrawerWidthPx\.value - 6\)/)
  assert.match(drawerSource, /right: `\$\{resizeHandleRightPx\}px`/)
  assert.match(drawerSource, /\.setting-drawer__subtitle \{[\s\S]*overflow-wrap: anywhere/)
  assert.match(drawerSource, /var\(--td-brand-color-7, var\(--td-brand-color\)\)/)
  assert.doesNotMatch(drawerSource, /color:\s*var\(--td-text-color-placeholder\)/)
  assert.doesNotMatch(
    drawerSource,
    /\.setting-drawer__subtitle \{[\s\S]*white-space: nowrap[\s\S]*\}/,
  )
})
