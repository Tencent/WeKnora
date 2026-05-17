<template>
  <div class="tenant-members">
    <div class="section-header">
      <h2>{{ $t('tenantMember.title') }}</h2>
      <p class="section-description">{{ $t('tenantMember.sectionDescription') }}</p>
    </div>

    <!-- Two-tab layout. The Members tab is the original UI verbatim; the
         Audit log tab is gated to Admin+ because it can leak denial
         histories and member-management activity that ordinary members
         shouldn't see. -->
    <t-tabs v-model="activeTab" placement="top" class="tenant-members-tabs">
      <t-tab-panel value="members" :label="$t('tenantMember.tabs.members')">
        <div class="members-tab-layout">
          <!-- Toolbar：统计一行、检索一行，避免单行挤压力过大 -->
          <div class="members-toolbar">
            <div class="toolbar-meta">
              <span class="toolbar-count">{{ $t('tenantMember.totalCount', { n: members.length }) }}</span>
              <span class="toolbar-meta-sep" aria-hidden="true" />
              <t-popup placement="bottom-start" trigger="hover" overlay-class-name="permissions-popup-overlay"
                :overlay-inner-style="permissionsPopupInnerStyle">
                <button type="button" class="permissions-trigger-btn" :aria-label="$t('tenantMember.permissions.title')"
                  :title="$t('tenantMember.permissions.iconHint')">
                  <t-icon name="info-circle" size="18px" />
                </button>
                <template #content>
                  <div class="permissions-compact permissions-compact--popover">
                    <div class="permissions-compact-header">
                      <span class="permissions-compact-title">{{ $t('tenantMember.permissions.title') }}</span>
                      <span class="permissions-compact-desc">{{ $t('tenantMember.permissions.desc') }}</span>
                    </div>
                    <div class="permissions-compact-grid">
                      <div v-for="r in roleMatrixOrder" :key="r"
                        :class="['perm-role-block', r, { 'is-me': currentRole === r }]">
                        <div class="perm-role-tag">
                          <t-icon :name="roleMatrixIcon(r)" size="12px" />
                          <span>{{ $t('tenantMember.role.' + r) }}</span>
                          <span v-if="currentRole === r" class="me-badge">{{ $t('common.me') }}</span>
                        </div>
                        <div class="perm-items">
                          <span v-for="(perm, i) in roleMatrix[r]" :key="i"
                            :class="['perm-item', perm.has ? 'has' : 'no']">
                            <t-icon :name="perm.has ? 'check' : 'close'" size="12px" />
                            {{ $t('tenantMember.permissions.' + perm.key) }}
                          </span>
                        </div>
                      </div>
                    </div>
                  </div>
                </template>
              </t-popup>
            </div>
            <div class="toolbar-controls">
              <div class="toolbar-actions-bar">
                <div class="toolbar-search">
                  <t-input v-model="searchQuery" size="medium" :placeholder="$t('tenantMember.searchPlaceholder')"
                    clearable class="toolbar-search-input">
                    <template #prefix-icon><t-icon name="search" /></template>
                  </t-input>
                </div>
                <div class="toolbar-btn-group">
                  <t-button v-if="canManage" theme="primary" size="medium" @click="openAddDialog">
                    {{ $t('tenantMember.add.button') }}
                  </t-button>
                </div>
              </div>
            </div>
          </div>

          <!-- Loading -->
          <div v-if="loading" class="loading-inline">
            <t-loading size="small" />
            <span>{{ $t('tenantMember.loading') }}</span>
          </div>

          <!-- Error -->
          <div v-else-if="error" class="error-inline">
            <t-alert theme="error" :message="error">
              <template #operation>
                <t-button size="small" @click="loadMembers">{{ $t('tenantMember.retry') }}</t-button>
              </template>
            </t-alert>
          </div>

          <!-- Empty state when there are no members at all OR the search query
         filters everything out. -->
          <div v-else-if="filteredMembers.length === 0" class="empty-state">
            <t-empty :description="searchQuery
              ? $t('tenantMember.emptySearch', { q: searchQuery })
              : $t('tenantMember.empty')
              " />
          </div>

          <!-- Member table -->
          <div v-else class="data-table-shell">
            <t-table row-key="user_id" :data="filteredMembers" :columns="columns" size="medium" hover stripe>
              <template #member="{ row }">
                <div class="member-cell">
                  <span class="member-name">{{ memberPrimary(row) }}</span>
                  <span v-if="memberSecondary(row)" class="member-email">{{ memberSecondary(row) }}</span>
                </div>
              </template>
              <template #role="{ row }">
                <div class="role-cell">
                  <t-select v-if="canManage && row.user_id !== currentUserId" :model-value="row.role"
                    class="member-role-select" :options="roleOptions" size="small"
                    @change="(val: string) => onRoleChange(row, val)" />
                  <t-tag v-else :theme="roleTagTheme(row.role)" size="small">
                    {{ $t('tenantMember.role.' + row.role) }}
                  </t-tag>
                </div>
              </template>
              <template #joined_at="{ row }">{{ formatDate(row.joined_at) }}</template>
              <template #actions="{ row }">
                <t-button v-if="canManage && row.user_id !== currentUserId" theme="danger" variant="text" size="small"
                  @click="confirmRemove(row)">
                  {{ $t('tenantMember.remove.button') }}
                </t-button>
              </template>
            </t-table>
          </div>

          <!-- Add dialog. We use @confirm rather than :on-confirm so we can
         keep the dialog open on validation failure / API error and let
         the user retry without retyping. -->
          <t-dialog v-model:visible="addDialogVisible" :header="$t('tenantMember.add.dialogTitle')"
            :confirm-btn="{ content: $t('tenantMember.add.submit'), loading: adding }"
            :cancel-btn="{ content: $t('common.cancel') }" width="480px" @confirm="submitAdd">
            <t-form ref="addFormRef" :data="addForm" :rules="addFormRules" :label-width="80">
              <t-form-item :label="$t('tenantMember.add.emailLabel')" name="email">
                <t-input v-model="addForm.email" :placeholder="$t('tenantMember.add.emailPlaceholder')" clearable />
              </t-form-item>
              <t-form-item :label="$t('tenantMember.add.roleLabel')" name="role">
                <t-select v-model="addForm.role" :options="roleOptions" />
              </t-form-item>
            </t-form>
          </t-dialog>

        </div>
      </t-tab-panel>

      <!-- Audit log tab. Only rendered for Admin+ because the backend
           route is g.Admin()-gated; rendering it for lower roles would
           just produce an unhelpful 403. -->
      <t-tab-panel v-if="canViewAudit" value="audit" :label="$t('tenantMember.audit.tabLabel')">
        <div class="audit-panel">
          <div class="audit-header">
            <span class="audit-desc">{{ $t('tenantMember.audit.description') }}</span>
            <t-button variant="outline" :loading="auditLoading" @click="reloadAuditLog">
              {{ $t('tenantMember.audit.refresh') }}
            </t-button>
          </div>

          <div v-if="auditError" class="error-inline">
            <t-alert theme="error" :message="auditError">
              <template #operation>
                <t-button size="small" @click="reloadAuditLog">
                  {{ $t('tenantMember.retry') }}
                </t-button>
              </template>
            </t-alert>
          </div>

          <div v-else-if="!auditLoading && auditEntries.length === 0" class="empty-state">
            <t-empty :description="$t('tenantMember.audit.empty')" />
          </div>

          <div v-else class="data-table-shell">
            <t-table row-key="id" :data="auditEntries" :columns="auditColumns" size="medium" hover stripe>
              <template #created_at="{ row }">{{ formatDate(row.created_at) }}</template>
              <template #actor="{ row }">
                <span class="audit-actor">
                  {{ row.actor_user_id ? actorDisplayName(row.actor_user_id) : $t('tenantMember.audit.systemActor') }}
                  <span v-if="row.actor_role" class="audit-actor-role">
                    · {{ $t('tenantMember.role.' + row.actor_role) }}
                  </span>
                </span>
              </template>
              <template #action="{ row }">
                <t-tag :theme="auditActionTheme(row.action)" size="small">
                  {{ formatAuditAction(row.action) }}
                </t-tag>
              </template>
              <template #target="{ row }">
                <span class="audit-target">{{ formatAuditTarget(row) }}</span>
              </template>
              <template #request_path="{ row }">
                <span class="audit-path">
                  <span v-if="row.request_method" class="audit-method">{{ row.request_method }}</span>
                  {{ row.request_path || '-' }}
                </span>
              </template>
              <template #outcome="{ row }">
                <t-tag :theme="auditOutcomeTheme(row.outcome)" size="small">
                  {{ $t('tenantMember.audit.outcome.' + row.outcome) }}
                </t-tag>
              </template>
            </t-table>
          </div>

          <!-- Footer: load-more cursor. We avoid actual infinite-scroll
               (IntersectionObserver) because the table sits inside a
               scroll container that's already deep in the settings
               panel; an explicit button keeps behaviour predictable
               under unusual layouts. -->
          <div class="audit-footer">
            <t-button v-if="auditHasMore" size="small" variant="outline" :loading="auditLoading"
              @click="loadAuditLog(false)">
              {{ $t('tenantMember.audit.loadMore') }}
            </t-button>
            <span v-else-if="auditEntries.length > 0" class="audit-end">
              {{ $t('tenantMember.audit.end') }}
            </span>
          </div>
        </div>
      </t-tab-panel>
    </t-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listMembers,
  addMember,
  updateMemberRole,
  removeMember,
  type TenantMember,
  type TenantRole,
} from '@/api/tenant/members'
import {
  listAuditLog,
  type AuditLog,
  type AuditAction,
  type AuditOutcome,
} from '@/api/tenant/audit-log'

const { t, locale } = useI18n()
const authStore = useAuthStore()

/** 悬停层限制在视口内，内容由内部滚动 */
const permissionsPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(520px, calc(100vw - 24px))',
  maxWidth: 'min(520px, calc(100vw - 24px))',
  maxHeight: 'min(400px, 65vh)',
  overflow: 'hidden',
}

// State
const members = ref<TenantMember[]>([])
const loading = ref(false)
const error = ref('')
const adding = ref(false)
const addDialogVisible = ref(false)
const addFormRef = ref<any>(null)
const searchQuery = ref('')

// Tab state. Default to "members" — audit-log is the secondary view
// even for admins, who still mostly come here to manage members.
const activeTab = ref<'members' | 'audit'>('members')

// Audit-log state. Cursor-paginated by descending id; once
// `next_cursor` comes back as 0 we stop offering "load more". Page
// size is 50 (server default), large enough to feel responsive while
// keeping the table tractable on a small settings panel.
const auditEntries = ref<AuditLog[]>([])
const auditLoading = ref(false)
const auditError = ref('')
const auditCursor = ref<number>(0) // 0 = "from the top"
const auditHasMore = ref(true)
const auditLoadedOnce = ref(false)
const AUDIT_PAGE_SIZE = 50

// Add dialog model — reset on each open. Default role is contributor:
// inviting a fresh member with viewer is too restrictive for the
// expected "let them collaborate on KBs" use case, and admin/owner
// should be a deliberate promote step after the user accepts.
const addForm = reactive<{ email: string; role: TenantRole }>({
  email: '',
  role: 'contributor',
})

// Role-aware gates. The server enforces every mutation; UI gates here
// are presentational only, matching the security note in stores/auth.ts.
const currentRole = computed<TenantRole | ''>(() => (authStore.currentTenantRole || '') as TenantRole | '')
// Cross-tenant superusers (org-level operators) bypass the Owner gate
// on the server (see middleware/rbac.go RequireRole). The UI must
// mirror that or the buttons would be invisible to the exact admins
// who actually need them. Local Owners of their own tenant come in via
// the role branch.
const canManage = computed(
  () => currentRole.value === 'owner' || authStore.canAccessAllTenants === true,
)
// Admin+ (and cross-tenant superusers) can view the audit log. Mirrors
// the server's g.Admin() guard on /tenants/:id/audit-log so we don't
// render a tab that would just 403.
const canViewAudit = computed(
  () =>
    currentRole.value === 'owner' ||
    currentRole.value === 'admin' ||
    authStore.canAccessAllTenants === true,
)
const currentUserId = computed(() => authStore.user?.id ?? '')

// Use the active tenant id from the auth store; the route only allows
// :id == active tenant (auth middleware enforces membership), so we
// don't expose a tenant picker here.
const activeTenantId = computed(() => Number(authStore.currentTenantId ?? 0))

const roleOptions = computed(() => [
  { label: t('tenantMember.role.owner'), value: 'owner' },
  { label: t('tenantMember.role.admin'), value: 'admin' },
  { label: t('tenantMember.role.contributor'), value: 'contributor' },
  { label: t('tenantMember.role.viewer'), value: 'viewer' },
])

// Static role-permissions matrix. The keys reference i18n strings under
// `tenantMember.permissions.*` so each locale can rephrase per culture.
// Keep this aligned with the design-doc §4.3 matrix and the actual
// PR 2 enforcement; if a permission moves between roles, update both
// sides in the same PR.
type RolePerm = { key: string; has: boolean }
const roleMatrixOrder: TenantRole[] = ['owner', 'admin', 'contributor', 'viewer']
const roleMatrix: Record<TenantRole, RolePerm[]> = {
  owner: [
    { key: 'manageMembers', has: true },
    { key: 'manageTenantConfig', has: true },
    { key: 'manageInfra', has: true },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  admin: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: true },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  contributor: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: false },
    { key: 'createOwnKB', has: true },
    { key: 'readAll', has: true },
  ],
  viewer: [
    { key: 'manageMembers', has: false },
    { key: 'manageTenantConfig', has: false },
    { key: 'manageInfra', has: false },
    { key: 'createOwnKB', has: false },
    { key: 'readAll', has: true },
  ],
}

function roleMatrixIcon(role: TenantRole): string {
  switch (role) {
    case 'owner':
      return 'crown'
    case 'admin':
      return 'user-safety'
    case 'contributor':
      return 'edit'
    default:
      return 'browse'
  }
}

const columns = computed(() => [
  { colKey: 'member', title: t('tenantMember.columns.member'), ellipsis: true, minWidth: 160 },
  { colKey: 'role', title: t('tenantMember.columns.role'), width: 140 },
  { colKey: 'joined_at', title: t('tenantMember.columns.joinedAt'), width: 154 },
  { colKey: 'actions', title: t('tenantMember.columns.operations'), width: 88, align: 'right' },
])

function memberPrimary(row: { username?: string; email?: string }) {
  return row.username?.trim() || row.email?.trim() || '—'
}

function memberSecondary(row: { username?: string; email?: string }) {
  const name = row.username?.trim()
  const mail = row.email?.trim()
  if (name && mail) return mail
  return ''
}

const addFormRules = {
  email: [
    { required: true, message: t('tenantMember.errors.emailRequired'), trigger: 'blur' },
    { email: true, message: t('tenantMember.errors.emailFormat'), trigger: 'blur' },
  ],
  role: [{ required: true, message: t('tenantMember.errors.roleRequired'), trigger: 'change' }],
}

// Pretty role tag colour: Owner stands out, Admin is warning, the rest
// stay neutral so the table doesn't become a confetti cannon.
function roleTagTheme(role: TenantRole): 'primary' | 'warning' | 'success' | 'default' {
  switch (role) {
    case 'owner':
      return 'primary'
    case 'admin':
      return 'warning'
    case 'contributor':
      return 'success'
    default:
      return 'default'
  }
}

function formatDate(s: string | undefined): string {
  if (!s) return '-'
  try {
    const d = new Date(s)
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(d)
  } catch {
    return s
  }
}

// Client-side filter by username/email; case-insensitive substring
// match keeps the implementation tiny while covering the common
// "find this person quickly" use case. Server-side search is overkill
// for the expected per-tenant member counts.
const filteredMembers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return members.value
  return members.value.filter((m) => {
    return (
      (m.username || '').toLowerCase().includes(q) ||
      (m.email || '').toLowerCase().includes(q)
    )
  })
})

async function loadMembers() {
  if (!activeTenantId.value) {
    // No active tenant yet — keep silent; the watch below will retry
    // once the auth store finishes hydrating. Showing an error toast
    // on cold-mount would flash on every refresh.
    return
  }
  loading.value = true
  error.value = ''
  try {
    const resp = await listMembers(activeTenantId.value)
    if (resp.success && resp.data) {
      members.value = resp.data.members
    } else {
      error.value = resp.message || t('tenantMember.errors.generic')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenantMember.errors.generic')
  } finally {
    loading.value = false
  }
}

// ---- Audit-log helpers --------------------------------------------------

const auditColumns = computed(() => [
  { colKey: 'created_at', title: t('tenantMember.audit.columns.time'), width: 150 },
  { colKey: 'actor', title: t('tenantMember.audit.columns.actor'), minWidth: 140, ellipsis: true },
  { colKey: 'action', title: t('tenantMember.audit.columns.action'), width: 130 },
  { colKey: 'target', title: t('tenantMember.audit.columns.target'), minWidth: 140, ellipsis: true },
  { colKey: 'request_path', title: t('tenantMember.audit.columns.path'), minWidth: 140, ellipsis: true },
  { colKey: 'outcome', title: t('tenantMember.audit.columns.outcome'), width: 90 },
])

// Action chip colour: rejection events are loud (danger) so an
// operator can scan a chronological feed and immediately spot abuse;
// member adds are reassuring green; removals/role changes warning
// orange because they're worth a second look but aren't intrinsically
// suspicious.
function auditActionTheme(
  action: AuditAction,
): 'success' | 'warning' | 'danger' | 'primary' | 'default' {
  switch (action) {
    case 'rbac.access_denied':
      return 'danger'
    case 'rbac.member_added':
      return 'success'
    case 'rbac.member_removed':
    case 'rbac.member_left':
    case 'rbac.member_role_changed':
      return 'warning'
    default:
      return 'default'
  }
}

function auditOutcomeTheme(o: AuditOutcome): 'success' | 'danger' | 'default' {
  if (o === 'denied') return 'danger'
  if (o === 'success') return 'success'
  return 'default'
}

// Render a `rbac.member_added` action either via i18n (when the key
// exists) or as a humanised fallback. The fallback matters because
// future PRs may push new namespaces (e.g. `kb.shared`) that aren't
// in the locale file yet — we still want them to render readably.
function formatAuditAction(action: AuditAction): string {
  const key = `tenantMember.audit.action.${action}`
  const translated = t(key)
  if (translated && translated !== key) return translated
  return action
}

// Resolve a user id to a display name using the already-loaded member
// list. Falls back to the raw uuid when the actor is no longer a
// member (e.g. a long-since-removed contributor whose denial events
// are still on the feed).
function actorDisplayName(userId: string): string {
  const m = members.value.find((x) => x.user_id === userId)
  return m?.username || m?.email || userId
}

function formatAuditTarget(row: AuditLog): string {
  if (row.target_user_id) return actorDisplayName(row.target_user_id)
  if (row.target_id) {
    return row.target_type ? `${row.target_type}:${row.target_id}` : row.target_id
  }
  // Role-change details often carry old_role/new_role; surface that
  // inline so an operator doesn't have to expand the row to see
  // what actually changed.
  if (row.action === 'rbac.member_role_changed' && row.details && typeof row.details === 'object') {
    const d = row.details as Record<string, unknown>
    if (d.old_role && d.new_role) {
      return `${d.old_role} → ${d.new_role}`
    }
  }
  if (row.action === 'rbac.access_denied' && row.details && typeof row.details === 'object') {
    const d = row.details as Record<string, unknown>
    if (d.required_role) return t('tenantMember.audit.requiredRole', { role: d.required_role })
  }
  return '-'
}

// loadAuditLog fetches a page. `reset=true` discards the current
// list and starts from cursor=0. Used by the refresh button and the
// initial tab-switch trigger.
async function loadAuditLog(reset: boolean) {
  if (!activeTenantId.value || !canViewAudit.value) return
  if (auditLoading.value) return
  if (!reset && !auditHasMore.value) return

  auditLoading.value = true
  auditError.value = ''
  try {
    const resp = await listAuditLog(activeTenantId.value, {
      after_id: reset ? undefined : auditCursor.value || undefined,
      limit: AUDIT_PAGE_SIZE,
    })
    if (resp.success) {
      const rows = resp.data || []
      if (reset) {
        auditEntries.value = rows
      } else {
        auditEntries.value = [...auditEntries.value, ...rows]
      }
      auditCursor.value = resp.next_cursor || 0
      // The server returns next_cursor=0 when the page is empty OR
      // when the last row is the smallest possible id. Both mean
      // "stop paginating".
      auditHasMore.value = !!resp.next_cursor && rows.length > 0
      auditLoadedOnce.value = true
    } else {
      auditError.value = resp.message || t('tenantMember.errors.generic')
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 403) {
      auditError.value = t('tenantMember.audit.forbidden')
    } else {
      auditError.value = err?.message || t('tenantMember.errors.generic')
    }
  } finally {
    auditLoading.value = false
  }
}

function reloadAuditLog() {
  auditCursor.value = 0
  auditHasMore.value = true
  loadAuditLog(true)
}

// Lazy-load the audit log on first tab switch. Fetching it on every
// settings-panel mount would waste a request for the (common) case
// where the operator only wants to manage members.
watch(activeTab, (tab) => {
  if (tab === 'audit' && !auditLoadedOnce.value) {
    loadAuditLog(true)
  }
})

function openAddDialog() {
  addForm.email = ''
  addForm.role = 'contributor'
  addDialogVisible.value = true
}

async function submitAdd() {
  // t-form's validate returns true on success or an object of field
  // errors on failure. Anything other than `true` => keep dialog open
  // with the inline messages shown. The @confirm event lets us control
  // visibility manually; :on-confirm would close on every return.
  const valid = await addFormRef.value?.validate?.()
  if (valid !== true) return

  adding.value = true
  try {
    const resp = await addMember(activeTenantId.value, {
      email: addForm.email.trim(),
      role: addForm.role,
    })
    if (resp.success && resp.data) {
      members.value = [...members.value, resp.data]
      addDialogVisible.value = false
      MessagePlugin.success(t('tenantMember.add.success'))
    } else {
      MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
    }
  } catch (err: any) {
    // The axios interceptor already flattens errors to
    // { status, message, error?, ... }, so we read err.status directly
    // — err.response.status is undefined here.
    const status = err?.status
    if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.userNotFound'))
    } else if (status === 409) {
      MessagePlugin.error(t('tenantMember.errors.alreadyMember'))
    } else if (status === 400) {
      MessagePlugin.error(err?.message || t('tenantMember.errors.invalidRole'))
    } else {
      MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
    }
  } finally {
    adding.value = false
  }
}

async function onRoleChange(row: TenantMember, newRole: string) {
  const prev = row.role
  const next = newRole as TenantRole
  if (prev === next) return

  try {
    const resp = await updateMemberRole(activeTenantId.value, row.user_id, next)
    if (resp.success) {
      row.role = next
      MessagePlugin.success(t('tenantMember.roleChange.success'))
      return
    }
    MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
  } catch (err: any) {
    const status = err?.status
    if (status === 409) {
      MessagePlugin.error(t('tenantMember.errors.lastOwner'))
    } else if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.notFound'))
    } else {
      MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
    }
    // The t-select is bound via :model-value (one-way), so its rendered
    // value stays at `prev` automatically — no DOM hack needed.
  }
}

function confirmRemove(row: TenantMember) {
  // Use DialogPlugin.confirm rather than t-popconfirm so we can describe
  // the consequences in a multi-line modal (matches the pattern used in
  // ApiInfo.vue's reset-API-key flow).
  const dlg = DialogPlugin.confirm({
    header: t('tenantMember.remove.confirmTitle'),
    body: t('tenantMember.remove.confirmBody', { name: row.username || row.email }),
    confirmBtn: { content: t('tenantMember.remove.confirm'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      try {
        const resp = await removeMember(activeTenantId.value, row.user_id)
        if (resp.success) {
          members.value = members.value.filter((m) => m.user_id !== row.user_id)
          MessagePlugin.success(t('tenantMember.remove.success'))
        } else {
          MessagePlugin.error(resp.message || t('tenantMember.errors.generic'))
        }
      } catch (err: any) {
        const status = err?.status
        if (status === 409) {
          MessagePlugin.error(t('tenantMember.errors.lastOwner'))
        } else if (status === 404) {
          MessagePlugin.error(t('tenantMember.errors.notFound'))
        } else {
          MessagePlugin.error(err?.message || t('tenantMember.errors.generic'))
        }
      } finally {
        dlg.destroy()
      }
    },
    onClose: () => dlg.destroy(),
  })
}

// Re-load whenever the active tenant resolves (or changes via the
// tenant switcher). onMounted alone would race with auth-store
// hydration — currentTenantId is often 0 at the moment this component
// mounts on a cold reload.
watch(
  activeTenantId,
  (id) => {
    if (id) loadMembers()
  },
  { immediate: true },
)

onMounted(() => {
  if (activeTenantId.value) loadMembers()
})
</script>

<style lang="less" scoped>
.tenant-members {
  width: 100%;
}

.member-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: 2px 0;

  .member-name {
    font-weight: 500;
    font-size: 14px;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .member-email {
    font-size: 12px;
    line-height: 1.35;
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.section-header {
  margin-bottom: 20px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 6px 0;
    letter-spacing: -0.02em;
  }

  .section-description {
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
    margin: 0;
    max-width: 52rem;
  }
}

.tenant-members-tabs {
  :deep(.t-tabs__nav-scroll-container) {
    margin-bottom: 2px;
  }

  :deep(.t-tabs__nav-item) {
    height: 40px;
    line-height: 40px;
  }

  :deep(.t-tab-panel) {
    padding-top: 4px;
  }
}

.members-tab-layout {
  display: flex;
  flex-direction: column;
}

.members-toolbar {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin-bottom: 20px;
}

.toolbar-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  min-height: 32px;

  .toolbar-count {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-secondary);
  }

  .toolbar-meta-sep {
    flex-shrink: 0;
    width: 1px;
    height: 14px;
    background-color: var(--td-component-border);
    opacity: 0.72;
  }
}

.permissions-trigger-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}

.toolbar-controls {
  display: block;
}

.toolbar-actions-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;
  padding: 8px 12px;
  background-color: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-border);
  border-radius: 8px;
  box-sizing: border-box;
}

.toolbar-search {
  flex: 1 1 12rem;
  min-width: 0;
  max-width: 22rem;

  :deep(.t-input),
  .toolbar-search-input {
    width: 100%;
  }

  &:deep(.t-input__prefix)>.t-icon {
    align-self: center;
  }
}

.toolbar-btn-group {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  flex-shrink: 0;
}

@media (max-width: 560px) {
  .toolbar-actions-bar {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .toolbar-search {
    flex-basis: auto;
    max-width: none;
  }

  .toolbar-btn-group {
    justify-content: flex-end;
    flex-wrap: nowrap;
    gap: 8px;
  }
}

.data-table-shell {
  overflow-x: auto;
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke);
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    font-weight: 600;
    font-size: 13px;
  }

  &:deep(.t-table td),
  &:deep(.t-table th) {
    padding-top: 12px;
    padding-bottom: 12px;
  }

  /* 角色列：下拉不要超过单元格，避免外层 overflow-x 把右边框裁没 */
  &:deep(.role-cell) {
    display: flex;
    align-items: center;
    width: 100%;
    max-width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }

  &:deep(.member-role-select.t-select),
  &:deep(.member-role-select.t-select > .t-select__wrap) {
    flex: 1 1 auto;
    width: 100%;
    max-width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }
}

.permissions-compact {
  padding: 8px;

  .permissions-compact-header {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 16px;

    .permissions-compact-title {
      font-size: 14px;
      font-weight: 600;
      color: var(--td-text-color-primary);
    }

    .permissions-compact-desc {
      font-size: 13px;
      color: var(--td-text-color-secondary);
    }
  }

  .permissions-compact-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 12px;
  }

  .perm-role-block {
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    padding: 14px 16px;
    background: var(--td-bg-color-container);
    transition: all 0.2s ease;

    &.is-me {
      border-color: var(--td-brand-color);
      background: var(--td-brand-color-light);
    }

    .perm-role-tag {
      display: flex;
      align-items: center;
      gap: 6px;
      font-size: 14px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin-bottom: 12px;

      .me-badge {
        margin-left: auto;
        font-size: 12px;
        font-weight: 500;
        color: var(--td-brand-color);
        padding: 2px 8px;
        background: var(--td-brand-color-light);
        border-radius: 4px;
      }
    }

    .perm-items {
      display: flex;
      flex-direction: column;
      gap: 6px;

      .perm-item {
        display: flex;
        align-items: flex-start;
        gap: 6px;
        font-size: 13px;
        line-height: 1.5;

        .t-icon {
          margin-top: 2px;
          flex-shrink: 0;
        }

        &.has {
          color: var(--td-text-color-secondary);

          .t-icon {
            color: var(--td-brand-color);
          }
        }

        &.no {
          color: var(--td-text-color-disabled);

          .t-icon {
            color: var(--td-text-color-disabled);
          }
        }
      }
    }
  }

  /* Hover 弹出层：压扁占位 + 2×2 角色块 + 内部滚动 */
  &.permissions-compact--popover {
    padding: 10px 12px;
    margin: 0;
    max-height: min(392px, calc(65vh - 8px));
    overflow-x: hidden;
    overflow-y: auto;

    .permissions-compact-header {
      gap: 2px;
      margin-bottom: 10px;

      .permissions-compact-title {
        font-size: 13px;
      }

      .permissions-compact-desc {
        font-size: 11px;
        line-height: 1.4;
      }
    }

    .permissions-compact-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
    }

    .perm-role-block {
      padding: 8px 10px;
      border-radius: 6px;

      .perm-role-tag {
        font-size: 12px;
        margin-bottom: 6px;
        gap: 4px;

        .me-badge {
          font-size: 10px;
          padding: 1px 5px;
        }
      }

      .perm-items {
        gap: 3px;

        .perm-item {
          font-size: 11px;
          line-height: 1.35;
          gap: 4px;

          .t-icon {
            margin-top: 1px;
            flex-shrink: 0;
          }
        }
      }
    }
  }

  @media (max-width: 480px) {
    &.permissions-compact--popover .permissions-compact-grid {
      grid-template-columns: 1fr;
    }
  }
}

.loading-inline,
.error-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px 0 8px;
}

.empty-state {
  padding: 40px 0 16px;
  display: flex;
  justify-content: center;
}

.audit-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-top: 8px;
}

.audit-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--td-bg-color-secondarycontainer);
  padding: 12px 16px;
  border-radius: 8px;
  gap: 12px;

  .audit-desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }
}

.audit-actor {
  font-size: 13px;
  color: var(--td-text-color-primary);

  .audit-actor-role {
    color: var(--td-text-color-secondary);
    margin-left: 2px;
  }
}

.audit-target {
  font-size: 13px;
  color: var(--td-text-color-primary);
  word-break: break-all;
}

.audit-path {
  font-family: var(--td-font-family-mono, monospace);
  font-size: 12px;
  color: var(--td-text-color-secondary);

  .audit-method {
    display: inline-block;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin-right: 4px;
  }
}

.audit-footer {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 8px 0 4px;
  margin-top: 4px;

  .audit-end {
    font-size: 12px;
    color: var(--td-text-color-disabled);
  }
}
</style>
