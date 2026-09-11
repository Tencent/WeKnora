/**
 * Decision table for org-share / shared-agent permission grants on
 * knowledge bases.
 *
 * Cross-tenant KBs always carry an explicit effective permission
 * (`min(share.permission, org_role, tenant_role_cap)` resolved server-side,
 * surfaced as `permission` on shared cards and `my_permission` on the KB
 * detail payload). When such a grant exists it is the ONLY thing that
 * counts: the browsing user's local tenant role (e.g. being an admin of
 * their own personal workspace) must never upgrade it, because the
 * backend RBAC cap would 403 the mutation anyway — showing the buttons
 * just produces dead ends (#3098).
 */

/** Editor-level grant: knowledge/chunk mutations (upload, edit, delete chunks). */
export function permissionCanEditKB(permission: string | null | undefined): boolean {
  return permission === 'owner' || permission === 'admin' || permission === 'editor'
}

/** Admin-level grant: KB settings and destructive KB operations (delete). */
export function permissionCanManageKB(permission: string | null | undefined): boolean {
  return permission === 'owner' || permission === 'admin'
}
