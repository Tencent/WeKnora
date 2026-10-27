import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import {
  CUSTOMER_LANDING_PATH,
  CUSTOMER_MENU_PATHS,
  CUSTOMER_SETTINGS_SECTIONS,
  defaultLandingPath,
  isCustomerAllowedRoute,
  isCustomerMenuPath,
  isCustomerSettingsSection,
  isCustomerViewer,
  normalizeSettingsSection,
} from './customerAccess.ts'

/**
 * 客户访问控制（AC-11 / AC-12 / AC-13 / AC-14 / AC-23 过滤部分）。
 * 只测纯函数 + 路由守卫的静态接线；守卫本身依赖 Pinia / vue-router，不在 node 里挂载。
 */

test('isCustomerViewer is fail-closed: every non-admin non-empty role is a customer', () => {
  // 客户侧：viewer、contributor，以及将来新增但未列入管理员黑名单的角色。
  for (const role of ['viewer', 'contributor', 'future-role']) {
    assert.equal(isCustomerViewer({ currentTenantRole: role }), true, role)
    assert.equal(isCustomerViewer({ currentTenantRole: role, canAccessAllTenants: false }), true, role)
    assert.equal(isCustomerViewer({ currentTenantRole: role, canAccessAllTenants: null }), true, role)
  }

  // 只有明确的 admin / owner 才算管理员侧。
  for (const role of ['admin', 'owner']) {
    assert.equal(isCustomerViewer({ currentTenantRole: role, canAccessAllTenants: false }), false, role)
  }

  // 角色为空 / 空白 / 非字符串时鉴权态还没水合（D8），不能误判成客户。
  const withRole = (role: unknown) => isCustomerViewer({ currentTenantRole: role as string | null })
  for (const role of ['', '   ', undefined, null, 42, {}, []]) {
    assert.equal(withRole(role), false, `role=${String(role)}`)
  }

  // 系统管理员 / 跨空间权限始终是管理员视角，即使当前空间角色是 viewer。
  assert.equal(isCustomerViewer({ currentTenantRole: 'viewer', isSystemAdmin: true }), false)
  assert.equal(isCustomerViewer({ currentTenantRole: 'viewer', canAccessAllTenants: true }), false)
  assert.equal(isCustomerViewer({ currentTenantRole: 'viewer', isSystemAdmin: false, canAccessAllTenants: false }), true)
  assert.equal(isCustomerViewer(undefined), false)
  assert.equal(isCustomerViewer(null), false)
})

test('customer menu keeps only creatChat and the landing path is the chat page', () => {
  assert.deepEqual([...CUSTOMER_MENU_PATHS], ['creatChat'])
  assert.equal(isCustomerMenuPath('creatChat'), true)
  for (const path of ['knowledge-bases', 'agents', 'artifacts', 'automations', 'toolbox', 'organizations', 'settings', 'logout', '']) {
    assert.equal(isCustomerMenuPath(path), false, path)
  }
  assert.equal(CUSTOMER_LANDING_PATH, '/platform/creatChat')
})

test('customer settings whitelist is exactly general/userprofile/mymemory', () => {
  assert.deepEqual(
    [...CUSTOMER_SETTINGS_SECTIONS],
    ['general', 'userprofile', 'mymemory'],
  )
  for (const section of CUSTOMER_SETTINGS_SECTIONS) {
    assert.equal(isCustomerSettingsSection(section), true, section)
  }
  // 助手分区随 H2 一起从客户设置里移除。
  const hidden = [
    'assistant', 'models', 'ollama', 'vectorstore', 'parser', 'storage', 'websearch', 'chathistory',
    'memory', 'mcp', 'browserconnection', 'members', 'tenant', 'system', 'system-global',
    'sandbox', 'skills', 'envvars', 'weknoracloud',
  ]
  for (const section of hidden) {
    assert.equal(isCustomerSettingsSection(section), false, `${section} must stay hidden`)
  }
  assert.equal(isCustomerSettingsSection(undefined), false)
  assert.equal(isCustomerSettingsSection(''), false)
})

test('customer allowed routes: globalCreatChat/chat + whitelisted settings sections', () => {
  for (const name of ['globalCreatChat', 'chat']) {
    assert.equal(isCustomerAllowedRoute({ name, path: '/whatever' }), true, name)
  }
  // 知识库维度的对话路由已从白名单删除（M1 / AC-14）。
  assert.equal(isCustomerAllowedRoute({ name: 'kbCreatChat', path: '/whatever' }), false)
  assert.equal(isCustomerAllowedRoute({ name: 'settings', query: {} }), true, 'settings defaults to general')
  for (const section of CUSTOMER_SETTINGS_SECTIONS) {
    assert.equal(isCustomerAllowedRoute({ name: 'settings', query: { section } }), true, section)
  }
  for (const section of ['assistant', 'models', 'vectorstore', 'parser', 'envvars']) {
    assert.equal(isCustomerAllowedRoute({ name: 'settings', query: { section } }), false, section)
  }
  assert.equal(isCustomerAllowedRoute({ name: 'knowledgeBaseList', path: '/platform/knowledge-bases' }), false)
  assert.equal(isCustomerAllowedRoute({ name: 'agentList', path: '/platform/agents' }), false)
  assert.equal(isCustomerAllowedRoute({ name: 'organizationList', path: '/platform/organizations' }), false)
  assert.equal(isCustomerAllowedRoute({ name: 'systemSettings', path: '/platform/system/settings' }), false)
  assert.equal(isCustomerAllowedRoute(undefined), false)
  assert.equal(isCustomerAllowedRoute(null), false)
})

test('customer allowed routes also match raw chat paths and reject KB chat deep links', () => {
  for (const path of ['/platform/creatChat', '/platform/creatChat/', '/platform/chat/session-1', '/platform/chat/session-1/']) {
    assert.equal(isCustomerAllowedRoute({ path }), true, path)
  }
  // 知识库对话深链必须被拦下，统一回客户落点（M1）。
  for (const path of [
    '/platform/knowledge-bases/kb-1/creatChat',
    '/platform/knowledge-bases/kb-1/creatChat/',
    '/platform/knowledge-bases',
    '/platform/agents',
    '/platform',
    '/platform/settings',
  ]) {
    assert.equal(isCustomerAllowedRoute({ path }), false, path)
  }
})

test('T04 strict redirect set: tenant/system/integrations never count as allowed by their final URL', () => {
  // 这些路径在路由表里被 redirect 到 /platform/settings；客户不能因为最终落到 settings 就放行。
  const compat = [
    '/platform/tenant',
    '/platform/system',
    '/platform/system/settings',
    '/platform/system/admins',
    '/platform/system/queues',
    '/platform/integrations',
  ]
  for (const path of compat) {
    assert.equal(isCustomerAllowedRoute({ path }), false, `${path} must redirect to creatChat`)
  }
})

test('normalizeSettingsSection sends customers to general but leaves admins untouched', () => {
  const viewer = { currentTenantRole: 'viewer', canAccessAllTenants: false }
  for (const section of ['assistant', 'models', 'vectorstore', 'parser', 'envvars', 'members', 'weknoracloud']) {
    assert.equal(normalizeSettingsSection(viewer, section), 'general', section)
  }
  for (const section of CUSTOMER_SETTINGS_SECTIONS) {
    assert.equal(normalizeSettingsSection(viewer, section), section)
  }
  assert.equal(normalizeSettingsSection(viewer, ''), 'general')
  assert.equal(normalizeSettingsSection(viewer, undefined), 'general')
  // 管理员 deep-link 保持原分区
  for (const section of ['models', 'envvars', 'members']) {
    assert.equal(normalizeSettingsSection({ currentTenantRole: 'admin' }, section), section)
  }
  assert.equal(normalizeSettingsSection(null, 'envvars'), 'envvars')
})

test('defaultLandingPath keeps customers on creatChat and others on the caller fallback', () => {
  const viewer = { currentTenantRole: 'viewer', canAccessAllTenants: false }
  assert.equal(defaultLandingPath(viewer), CUSTOMER_LANDING_PATH)
  assert.equal(defaultLandingPath(viewer, '/platform/knowledge-bases'), CUSTOMER_LANDING_PATH)
  assert.equal(defaultLandingPath({ currentTenantRole: 'admin' }), '/platform/knowledge-bases')
  assert.equal(defaultLandingPath({ currentTenantRole: 'owner' }, '/platform/agents'), '/platform/agents')
  assert.equal(defaultLandingPath(undefined), '/platform/knowledge-bases')
  assert.equal(defaultLandingPath({ currentTenantRole: '' }, '/x'), '/x')
})

// ─── 路由守卫接线（静态断言） ───────────────────────────────────────────────
const routerSource = readFileSync(new URL('../router/index.ts', import.meta.url), 'utf8')

test('router guard wires the strict redirectedFrom check and the settings exception', () => {
  assert.match(routerSource, /const redirectedFrom = to\.redirectedFrom/)
  assert.match(routerSource, /redirectedFrom\.path !== '\/platform\/settings'/)
  assert.match(routerSource, /!isCustomerAllowedRoute\(redirectedFrom\)/)
  assert.match(routerSource, /next\(CUSTOMER_LANDING_PATH\)/)
  // 客户非法 settings section 先归一化到 general，不能被当成兼容路径踢回对话页。
  assert.match(routerSource, /normalizeCustomerSettingsSection\(authStore, rawSection\)/)
  assert.match(routerSource, /section: normalizeCustomerSettingsSection/)
  assert.match(routerSource, /replace: true/)
})

test('router guard keeps landing defaults on defaultLandingPath and applies customer brand', () => {
  assert.match(routerSource, /function landingPathForCurrentAuth\(\): string \{[\s\S]*?defaultLandingPath\(useAuthStore\(\), '\/platform\/knowledge-bases'\)/)
  assert.match(routerSource, /applyDocumentBrandForRole\(isCustomerViewer\(authStore\)\)/)
  assert.match(routerSource, /if \(isCustomerViewer\(authStore\)\) \{/)
})

test('T08 exception: settings?section=envvars stays in settings and normalizes to general', () => {
  // 守卫排除 settings 自身作为 redirectedFrom，因此归一化那一跳不会被当成兼容路径。
  assert.match(routerSource, /to\.path !== CUSTOMER_LANDING_PATH[\s\S]*?redirectedFrom\.path !== '\/platform\/settings'/)
  const viewer = { currentTenantRole: 'viewer', canAccessAllTenants: false }
  assert.equal(normalizeSettingsSection(viewer, 'envvars'), 'general')
  assert.equal(isCustomerAllowedRoute({ name: 'settings', query: { section: 'envvars' } }), false)
})
