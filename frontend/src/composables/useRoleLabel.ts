import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'

/**
 * Format a tenant role enum value ('viewer' | 'contributor' | 'admin' | 'owner')
 * into the user-facing label declared under tenantMember.role.* in the i18n
 * bundle. Falls back to the raw role string when the locale does not carry
 * the key — same behaviour as the inline helper this used to live in
 * (UserMenu.vue), now extracted so role-aware UI gates across views can
 * share one implementation.
 *
 * `roleIcon(role)` returns a TDesign icon name suitable for prefixing the
 * role label (e.g. in tenant switcher rows). The mapping is intentionally
 * limited to icons already known to ship in this codebase to avoid a
 * runtime "icon not found" footgun on uncommon role names.
 */
export function useRoleLabel() {
  const { t } = useI18n()
  const formatRole = (role: string | null | undefined): string => {
    if (!role) return ''
    const key = `tenantMember.role.${role}`
    const label = t(key)
    return label === key ? role : label
  }
  const ROLE_ICONS: Record<string, string> = {
    owner: 'secured',
    admin: 'user-circle',
    contributor: 'edit',
    viewer: 'browse',
  }
  const roleIcon = (role: string | null | undefined): string =>
    (role && ROLE_ICONS[role]) || ''
  return { formatRole, roleIcon }
}

/**
 * Derive home-tenant identity helpers off the auth store. The home tenant
 * is the tenant the user was registered into — the row id stored on the
 * users table at signup time, exposed via `authStore.user.tenant_id` and
 * never mutated by /auth/switch-tenant.
 *
 * IMPORTANT: do NOT read `authStore.tenant?.id` here. That field is the
 * *active* tenant returned by /auth/me (see internal/handler/auth.go
 * GetCurrentUser — it deliberately reflects the X-Tenant-ID override).
 * After a tenant switch, `authStore.tenant.id` becomes the peer tenant
 * id; treating it as "home" makes the home badge follow the user across
 * switches and incorrectly mark whichever tenant they're currently in.
 *
 * `activeTenantId` is the currently selected tenant (= home id when no
 * override is in play), used by `isHomeTenantActive` to answer "am I
 * sitting in my home tenant right now?".
 */
export function useHomeTenant() {
  const authStore = useAuthStore()
  const homeTenantId = computed<number | null>(() => {
    const raw = authStore.user?.tenant_id
    if (raw === null || raw === undefined || raw === '') return null
    const n = Number(raw)
    return Number.isFinite(n) && n > 0 ? n : null
  })
  const activeTenantId = computed<number | null>(() => authStore.effectiveTenantId)
  const isHomeTenantActive = computed(() => {
    const home = homeTenantId.value
    const active = activeTenantId.value
    return home !== null && active !== null && home === active
  })
  const isHomeTenant = (id: number | string | null | undefined): boolean => {
    if (id === null || id === undefined || id === '') return false
    const home = homeTenantId.value
    return home !== null && Number(id) === home
  }
  return { homeTenantId, activeTenantId, isHomeTenantActive, isHomeTenant }
}
