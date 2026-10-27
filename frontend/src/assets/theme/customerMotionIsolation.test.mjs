import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

/**
 * 客户版动效隔离（AC-27 / AC-28 / C6 / C8）。
 *
 * 关键约束：
 * - 客户动效只挂在 html[data-customer="true"] 下，管理员端一条也命不中；
 * - 五类容器动效齐全，时长走 --app-motion-* 令牌；
 * - prefers-reduced-motion: reduce 时全部退化；
 * - morphicons 只用在三处，图标从 lucide 具名导入。
 */

const motionCss = readFileSync(new URL('./customer-motion.css', import.meta.url), 'utf8')
const brandCss = readFileSync(new URL('./brand.css', import.meta.url), 'utf8')
const packageJson = readFileSync(new URL('../../../package.json', import.meta.url), 'utf8')
const lineSelector = readFileSync(new URL('../../components/customer/LineSelector.vue', import.meta.url), 'utf8')
const themeToggle = readFileSync(new URL('../../components/customer/BrandThemeToggle.vue', import.meta.url), 'utf8')
const topBar = readFileSync(new URL('../../components/customer/CustomerTopBar.vue', import.meta.url), 'utf8')
const menuVue = readFileSync(new URL('../../components/menu.vue', import.meta.url), 'utf8')
const inputField = readFileSync(new URL('../../components/Input-field.vue', import.meta.url), 'utf8')

function stripComments(text) {
  return text
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/(^|[ \t])\/\/[^\n]*/gm, '')
}

function selectors(css) {
  const out = []
  const clean = stripComments(css)
  for (const match of clean.matchAll(/([^{}]+)\{/g)) {
    const raw = match[1].trim()
    if (!raw || raw.startsWith('@')) continue
    out.push(raw)
  }
  return out
}

function assertCustomerScoped(css, file, prefix) {
  for (const selector of selectors(css)) {
    for (const part of selector.split(',')) {
      const trimmed = part.trim()
      if (!trimmed || trimmed === 'from' || trimmed === 'to' || /^\d+%$/.test(trimmed)) continue
      assert.ok(
        trimmed.startsWith(prefix),
        `${file}: selector is not customer-scoped: ${trimmed}`,
      )
    }
  }
}

test('every customer-motion.css rule is scoped to html[data-customer="true"]', () => {
  assertCustomerScoped(motionCss, 'customer-motion.css', 'html[data-customer="true"]')
})

test('every brand.css rule is scoped to the customer attribute', () => {
  for (const selector of selectors(brandCss)) {
    for (const part of selector.split(',')) {
      const trimmed = part.trim()
      if (!trimmed) continue
      assert.ok(
        trimmed.includes('[data-customer="true"]'),
        `brand.css: selector leaks outside customer scope: ${trimmed}`,
      )
    }
  }
})

test('customer-motion.css contains all five container-motion categories', () => {
  for (const token of [
    'settings-modal-shell-enter-active', // 1 个人设置弹窗开合
    'settings-modal-shell-leave-active',
    'customer-page-enter-active', // 4 页面切换
    'customer-page-leave-active',
    '@keyframes customer-page-in',
    'settings-modal-shell .section', // 4b 设置分区切换
    '@keyframes customer-section-in',
    '.msg_list.customer-session-leaving', // 4c 历史会话切换
  ]) {
    assert.ok(motionCss.includes(token), `customer-motion.css missing ${token}`)
  }
  // 2 产线下拉在 LineSelector.vue；5 会话列表收放在 menu.vue
  // （展开态 = logo 行折叠按钮，收起态 = .sidebar-float-rail 浮出栏，U4 后顶栏不再放开关）
  assert.match(lineSelector, /\.line-dropdown-enter-active/)
  assert.match(lineSelector, /\.line-dropdown-leave-active/)
  assert.match(menuVue, /&\.aside_box--customer:not\(\.aside_box--resizing\)/)
  assert.match(menuVue, /&\.aside_box--customer-hidden/)
  assert.match(menuVue, /<div v-if="isCustomer" class="sidebar-float-rail"/)
  assert.match(menuVue, /&\.sidebar-float-rail--visible\s*\{[^}]*?width: 44px/)
})

test('motion durations use design tokens, not literal ms/s values', () => {
  const clean = stripComments(motionCss)
  const literalTransitions = clean.match(/transition[^;{]*?(?<![\d.])(?:0?\.\d+s|\d+ms)/g) ?? []
  assert.deepEqual(literalTransitions, [], `literal motion durations found: ${literalTransitions.join(', ')}`)
  assert.match(motionCss, /var\(--app-motion-(?:instant|fast|base)\)/)
  // 0.26s 侧栏节奏由 base × 1.3 得出，仍不写字面量；
  // U4 后只存在于 menu.vue（aside 与浮出栏各一处），顶栏已无动效。
  assert.match(menuVue, /&\.aside_box--customer:not\(\.aside_box--resizing\)\s*\{[^}]*?calc\(var\(--app-motion-base\) \* 1\.3\)/)
  assert.match(menuVue, /\.sidebar-float-rail\s*\{[^}]*?calc\(var\(--app-motion-base\) \* 1\.3\)/)
  assert.doesNotMatch(stripComments(topBar), /--app-motion-|transition\s*:/)
})

test('prefers-reduced-motion falls back to no animation everywhere', () => {
  assert.match(motionCss, /@media \(prefers-reduced-motion: reduce\)/)
  assert.match(motionCss, /transition: none/)
  assert.match(motionCss, /animation: none/)
  // 起始态的透明/位移也要复位，否则零时长下仍会闪一帧。
  assert.match(motionCss, /\.settings-modal-shell-enter-from[\s\S]*?opacity: 1;[\s\S]*?transform: none;/)
  for (const [name, source] of [
    ['LineSelector.vue', lineSelector],
    ['BrandThemeToggle.vue', themeToggle],
    ['menu.vue', menuVue],
  ]) {
    assert.match(stripComments(source), /@media \(prefers-reduced-motion: reduce\)/, `${name} must respect reduced motion`)
  }
  // U4 浮出栏：reduced-motion 下宽度过渡退化为 none（menu.vue 自带退化块）。
  assert.match(menuVue, /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.sidebar-float-rail\s*\{[^}]*?transition: none/)
  // 顶栏开关移除后 CustomerTopBar 已无任何动效，没有需要退化的东西。
  assert.doesNotMatch(stripComments(topBar), /transition|animation/)
})

test('morphicons is limited to the three whitelisted morphs from lucide named imports', () => {
  // 依赖白名单：只有 morphicons + lucide，没有 lucide-react / React / Svelte 入口。
  assert.match(packageJson, /"morphicons": "\^2\.0\.2"/)
  assert.match(packageJson, /"lucide": "\^1\.61\.0"/)
  assert.doesNotMatch(packageJson, /lucide-react/)

  assert.match(lineSelector, /import \{ MorphIcon \} from 'morphicons\/vue'/)
  assert.match(lineSelector, /import \{ ChevronDown, ChevronUp \} from 'lucide'/)
  assert.match(themeToggle, /import \{ MorphIcon \} from 'morphicons\/vue'/)
  assert.match(themeToggle, /import \{ Moon, Sun \} from 'lucide'/)
  assert.match(inputField, /import \{ MorphIcon \} from 'morphicons\/vue'/)
  assert.match(inputField, /ArrowUp,\s*Check/) // lucide 具名导入（多行 import）

  for (const source of [lineSelector, themeToggle, inputField]) {
    assert.doesNotMatch(source, /from 'lucide-react'/)
    assert.doesNotMatch(source, /import \* as lucide/)
    assert.doesNotMatch(source, /morphicons\/(react|svelte)/)
    assert.match(source, /reduced-motion="user"/)
    assert.match(source, /:stroke-width="2"/)
  }
})

test('sidebar toggle uses i18n labels, memory-only persistence and inert when collapsed', () => {
  // U4：收起/展开入口都在侧栏自身。展开态 = logo 行折叠按钮（i18n title）；
  // 收起态 = 侧栏右缘浮出栏（仅客户渲染，管理员不出现，C6）。
  assert.match(menuVue, /<div class="sidebar-toggle" @click="toggleSidebar" :title="t\('menu\.collapseSidebar'\)"/)
  // F1（R3）：展开态收起按钮键盘可达——tabindex/role/aria-label/aria-expanded 均为客户条件绑定，
  // 管理员分支为 undefined（Vue 不渲染，DOM 属性逐位不变，C6）；Enter/Space 走
  // toggleSidebarFromKeyboard，函数内 isCustomer 守卫兜底（管理员态本就无 tabindex 不可聚焦）。
  assert.match(menuVue, /<div class="sidebar-toggle"[\s\S]{0,300}?:tabindex="isCustomer \? 0 : undefined"/)
  assert.match(menuVue, /:role="isCustomer \? 'button' : undefined"/)
  assert.match(menuVue, /:aria-label="isCustomer \? t\('menu\.collapseSidebar'\) : undefined"/)
  assert.match(menuVue, /:aria-expanded="isCustomer \? !uiStore\.sidebarCollapsed : undefined"/)
  assert.match(menuVue, /@keydown\.enter\.prevent="toggleSidebarFromKeyboard"/)
  assert.match(menuVue, /@keydown\.space\.prevent="toggleSidebarFromKeyboard"/)
  assert.match(menuVue, /const toggleSidebarFromKeyboard = \(\) => \{\s*if \(!isCustomer\.value\) return;/)
  assert.match(menuVue, /<div v-if="isCustomer" class="sidebar-float-rail"/)
  assert.match(menuVue, /:aria-label="t\('brand\.sidebarShow'\)"/)
  assert.match(menuVue, /:aria-expanded="false"/)
  // 浮出栏按钮只改内存态，不覆盖同浏览器管理员的 sidebar_collapsed（M4）。
  assert.match(menuVue, /@click="uiStore\.expandSidebar\(\{ persist: false \}\)"/)
  // 浮出栏与收起的侧栏互斥：侧栏展开时浮出栏 inert + aria-hidden，不占焦点与点击。
  assert.match(menuVue, /:inert="!uiStore\.sidebarCollapsed"/)
  assert.match(menuVue, /:aria-hidden="!uiStore\.sidebarCollapsed \? 'true' : undefined"/)
  assert.doesNotMatch(menuVue, /隐藏会话列表|显示会话列表/)
  assert.doesNotMatch(topBar, /隐藏会话列表|显示会话列表/)
  // 刷新后默认展开（AC-29），只在客户组件里重置，不影响管理员的持久化状态。
  assert.match(topBar, /onMounted\(\(\) => \{[\s\S]*?uiStore\.sidebarCollapsed[\s\S]*?uiStore\.expandSidebar\(\{ persist: false \}\)/)

  // M6：收起后侧栏根节点 inert + aria-hidden，键盘不可进入。
  assert.match(menuVue, /:inert="isCustomer && uiStore\.sidebarCollapsed"/)
  assert.match(menuVue, /:aria-hidden="isCustomer && uiStore\.sidebarCollapsed \? 'true' : undefined"/)

  // M3：6 个 locale 都补齐了 brand.sidebarShow。
  // F2（R3）：brand.sidebarHide 死键已从 6 个 locale 删除（组件零引用）——反向断言防复活。
  for (const locale of ['zh-CN', 'zh-TW', 'en-US', 'ja-JP', 'ko-KR', 'ru-RU']) {
    const source = readFileSync(new URL(`../../i18n/locales/${locale}.ts`, import.meta.url), 'utf8')
    assert.match(source, /sidebarShow:/, `${locale} missing brand.sidebarShow`)
    assert.doesNotMatch(source, /sidebarHide:/, `${locale} still has dead brand.sidebarHide`)
  }
})
