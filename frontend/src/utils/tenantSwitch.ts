// Tenant-switch navigation helper.
//
// Switching the active tenant triggers a full page reload so every cached
// store / open SSE stream / in-flight request gets re-keyed under the new
// tenant. The catch: if the user is currently sitting on a route whose URL
// embeds a tenant-scoped resource id (KB id, session id), reloading lands
// them on a 403/empty page in the new tenant — visible to the user as a
// blank screen. For those routes we redirect to the platform default
// landing page instead, which is always safe regardless of role.
//
// Anything not on this list keeps its current path on reload (settings,
// agent list, KB list, etc. — they re-fetch under the new tenant
// naturally).

const TENANT_SCOPED_ROUTE_PATTERNS: RegExp[] = [
  /^\/platform\/knowledge-bases\/[^/]+$/,             // /platform/knowledge-bases/:kbId
  /^\/platform\/knowledge-bases\/[^/]+\/creatChat$/,  // /platform/knowledge-bases/:kbId/creatChat
  /^\/platform\/chat\/[^/]+$/,                        // /platform/chat/:chatid
]

const SAFE_FALLBACK_PATH = '/platform/knowledge-bases'

/**
 * Return the URL to navigate to after a tenant switch. If the current path
 * embeds a tenant-scoped id, returns the safe fallback; otherwise returns
 * null to signal "reload current path".
 */
export function tenantSwitchTargetPath(currentPath: string): string | null {
  const isScoped = TENANT_SCOPED_ROUTE_PATTERNS.some(re => re.test(currentPath))
  return isScoped ? SAFE_FALLBACK_PATH : null
}

/**
 * Perform the post-switch navigation. Either redirect to the safe fallback
 * (if the current page is tenant-scoped) or hard-reload the current page.
 */
export function navigateAfterTenantSwitch(): void {
  const target = tenantSwitchTargetPath(window.location.pathname)
  if (target !== null) {
    window.location.href = target
  } else {
    window.location.reload()
  }
}
