// Rich "you're now in {workspace} as {role}" notification for the
// post-login moment. Shared between the password (views/auth/Login.vue)
// and OIDC (App.vue handleGlobalOIDCCallback) login paths so the two
// flows feel identical to the user.
//
// Kept as a free function — not a composable — because the caller
// already has `t`, `formatRole` and `roleIcon` in scope from useI18n /
// useRoleLabel and there is no per-instance state worth tracking.

import { NotifyPlugin } from 'tdesign-vue-next'
import { renderWorkspaceNotifyContent } from './workspaceNotifyContent'

type Translator = (key: string, params?: Record<string, unknown>) => string
type RoleFormatter = (role: string | null | undefined) => string
type RoleIconResolver = (role: string | null | undefined) => string

interface LoginResponseLike {
  // Password-login response uses `active_tenant`; the OIDC callback
  // response uses `tenant` (legacy backward-compat name on the Go side).
  // Accept either so callers don't have to normalise.
  active_tenant?: { id?: number | string; name?: string }
  tenant?: { id?: number | string; name?: string }
  memberships?: Array<{ tenant_id?: number | string; role?: string }>
}

export function notifyLoginSuccess(
  response: LoginResponseLike | null | undefined,
  t: Translator,
  formatRole: RoleFormatter,
  roleIcon: RoleIconResolver,
): void {
  const activeTenant = response?.active_tenant || response?.tenant
  if (!activeTenant) return

  const tenantName = activeTenant.name || String(activeTenant.id || '')
  const activeTenantId = Number(activeTenant.id)
  const membership = Array.isArray(response?.memberships)
    ? response!.memberships!.find((m) => Number(m?.tenant_id) === activeTenantId)
    : null
  const roleEnum = membership?.role
  const roleLabel = roleEnum ? formatRole(roleEnum) : ''
  const roleIconName = roleEnum ? roleIcon(roleEnum) : ''

  // Fetch the template without interpolation so the workspace / role
  // placeholders survive and the renderer can swap them for styled chips.
  // vue-i18n returns the message verbatim when the named keys aren't
  // provided in the params bag.
  const templateKey = roleLabel
    ? 'auth.loginSuccessContentWithRole'
    : 'auth.loginSuccessContent'

  NotifyPlugin.success({
    title: t('auth.loginSuccessTitle'),
    content: renderWorkspaceNotifyContent({
      template: t(templateKey),
      name: tenantName,
      roleLabel: roleLabel || undefined,
      roleEnum: roleEnum || undefined,
      roleIconName: roleIconName || undefined,
    }),
    duration: 6000,
    closeBtn: true,
  })
}
