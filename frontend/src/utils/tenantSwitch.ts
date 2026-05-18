// Tenant-switch navigation helper.
//
// Switching the active tenant always lands the user on the platform's KB
// list. 之前是「在当前路径 reload」+ 个别敏感路径回退到 KB 列表，但即便不带
// resource id 的页面（设置、Agent 列表等）reload 后也常常因为新租户下没有
// 对应数据出现空状态，体验跟跳到固定首页其实差不多——干脆统一跳到 KB 列表，
// 用一次 full navigation 把所有 store / SSE / 请求都重置一遍。

const SAFE_FALLBACK_PATH = '/platform/knowledge-bases'

/**
 * Return the URL to navigate to after a tenant switch. 目前始终返回 KB 列表
 * 作为登陆页，保留函数签名是为了未来需要按路由做特殊处理时留个口子。
 */
export function tenantSwitchTargetPath(_currentPath: string): string {
  return SAFE_FALLBACK_PATH
}

/**
 * Perform the post-switch navigation. 统一跳到 KB 列表。
 */
export function navigateAfterTenantSwitch(): void {
  window.location.href = tenantSwitchTargetPath(window.location.pathname)
}
