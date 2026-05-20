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
// TemplateResolver returns the raw i18n message verbatim — placeholders
// like `{name}` are left untouched. Pass `tm` from useI18n (which does
// no interpolation), not `t` (which would replace unspecified named
// placeholders with empty strings and strand the renderer with nothing
// to split on).
type TemplateResolver = (key: string) => unknown
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
  tm: TemplateResolver,
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

  const templateKey = roleLabel
    ? 'auth.loginSuccessContentWithRole'
    : 'auth.loginSuccessContent'
  const rawTemplate = tm(templateKey)
  const template = typeof rawTemplate === 'string' ? rawTemplate : ''

  NotifyPlugin.success({
    title: t('auth.loginSuccessTitle'),
    content: renderWorkspaceNotifyContent({
      template,
      name: tenantName,
      roleLabel: roleLabel || undefined,
      roleEnum: roleEnum || undefined,
      roleIconName: roleIconName || undefined,
    }),
    duration: 6000,
    closeBtn: true,
  })
}
