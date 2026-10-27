/**
 * 客户视角的界面裁剪规则。
 *
 * 这些函数只决定「界面上显示哪些入口、点进来落到哪一页」，不做权限判定：
 * 真正的权限仍以后端 RBAC 为准，绕过界面直接调受限接口会收到 403（见 C4 / AC-15）。
 * 因此这里的措辞统一用「可见性 / 裁剪」，不要表述为安全层面的结论。
 *
 * 纯函数、无 Vue / Pinia 依赖，入参只取需要的字段，方便测试阶段直接传对象。
 */

/** 判定客户视角只需要这三个字段，避免把整个 auth store 拖进纯函数。 */
export interface CustomerAuthLike {
  currentTenantRole?: string | null
  canAccessAllTenants?: boolean | null
  /** 平台级系统管理员，独立于当前空间角色（auth store 的 isSystemAdmin）。 */
  isSystemAdmin?: boolean | null
}

/**
 * 只有明确的 admin / owner 才算管理员。
 * 其余非空角色（viewer、contributor，以及将来新增的角色）一律按客户处理，
 * 避免以后新增角色时默认露出管理员界面（fail-closed）。
 */
const ADMIN_TENANT_ROLES: readonly string[] = ['admin', 'owner']

/** 客户登录后的统一落点。 */
export const CUSTOMER_LANDING_PATH = '/platform/creatChat'

/** 客户侧边栏保留的菜单项（历史会话由 menu.vue 独立渲染，不在这里）。 */
export const CUSTOMER_MENU_PATHS: readonly string[] = ['creatChat']

/** 客户个人设置可见的分区：常规 / 账号 / 记忆。envvars（沙箱密钥）与助手刻意不包含。 */
export const CUSTOMER_SETTINGS_SECTIONS: readonly string[] = [
  'general',
  'userprofile',
  'mymemory',
]

/** 客户对话相关路由名。 */
const CUSTOMER_CHAT_ROUTE_NAMES = ['globalCreatChat', 'chat'] as const

/** 对话相关路径（按名字判断失败时兜底，例如外部直接拼 URL）。 */
const CUSTOMER_CHAT_PATH_PATTERNS: readonly RegExp[] = [
  /^\/platform\/creatChat\/?$/,
  /^\/platform\/chat\/[^/]+\/?$/,
]

/**
 * 是否客户视角：当前空间角色不是明确的 admin/owner，
 * 且不是系统管理员、也不具备跨空间权限。
 *
 * 角色为空时返回 false —— 鉴权态还没水合（D8）时不能把管理员误判成客户。
 * 其余非空角色一律按客户处理（含 viewer/contributor 和未来新增角色），
 * 这样新增角色不会默认露出管理员界面。
 */
export function isCustomerViewer(auth?: CustomerAuthLike | null): boolean {
  if (!auth) return false
  if (auth.isSystemAdmin === true) return false
  if (auth.canAccessAllTenants === true) return false
  const role = auth.currentTenantRole
  if (typeof role !== 'string' || role.trim() === '') return false
  return !ADMIN_TENANT_ROLES.includes(role)
}

export function isCustomerMenuPath(path: string): boolean {
  return CUSTOMER_MENU_PATHS.includes(path)
}

export function isCustomerSettingsSection(section: unknown): boolean {
  return typeof section === 'string' && CUSTOMER_SETTINGS_SECTIONS.includes(section)
}

/** 路由对象里只需要名字、路径和 query.section，测试可直接传普通对象。 */
export interface CustomerRouteLike {
  name?: unknown
  path?: string
  query?: Record<string, unknown>
}

function routeName(to: CustomerRouteLike): string {
  return typeof to.name === 'string' ? to.name : ''
}

function routeSettingsSection(to: CustomerRouteLike): string | null {
  const raw = to.query?.section
  if (typeof raw === 'string' && raw !== '') return raw
  if (Array.isArray(raw) && typeof raw[0] === 'string' && raw[0] !== '') return raw[0]
  return null
}

/**
 * 客户允许停留的路由：新对话 / 历史会话与个人设置的白名单分区。
 * settings 不带 section 时按默认「常规」处理。
 * 知识库维度的 creatChat 不在白名单内，会统一回到客户落点（AC-14）。
 */
export function isCustomerAllowedRoute(to?: CustomerRouteLike | null): boolean {
  if (!to) return false

  const name = routeName(to)
  if ((CUSTOMER_CHAT_ROUTE_NAMES as readonly string[]).includes(name)) return true
  if (name === 'settings') {
    return isCustomerSettingsSection(routeSettingsSection(to) ?? 'general')
  }

  const path = typeof to.path === 'string' ? to.path : ''
  return CUSTOMER_CHAT_PATH_PATTERNS.some((pattern) => pattern.test(path))
}

/**
 * 客户带到非法 settings section 时归一化为「常规」；管理员原样返回。
 * 空 section 也按「常规」处理。
 */
export function normalizeSettingsSection(
  auth: CustomerAuthLike | null | undefined,
  section?: unknown,
): string {
  const raw = typeof section === 'string' && section.trim() !== '' ? section : 'general'
  if (!isCustomerViewer(auth)) return raw
  return isCustomerSettingsSection(raw) ? raw : 'general'
}

/**
 * 默认落地页：客户回对话页，其他人用调用方给的 fallback。
 * fallback 由调用方传，避免这里绑死某个后台页面。
 */
export function defaultLandingPath(
  auth: CustomerAuthLike | null | undefined,
  fallback: string = '/platform/knowledge-bases',
): string {
  return isCustomerViewer(auth) ? CUSTOMER_LANDING_PATH : fallback
}
