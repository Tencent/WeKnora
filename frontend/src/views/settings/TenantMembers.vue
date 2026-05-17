<template>
  <div class="tenant-members">
    <!-- Section header. The (i) permission speed-look popover lives
         next to the title so it reads as meta-info about *this
         section*. The audit-log entry sits on the right of the header
         row — secondary navigation that opens the audit drawer; gated
         to Admin+ so non-managers don't see a button they can't use. -->
    <div class="section-header">
      <div class="section-header-row">
        <div class="section-header-titlewrap">
          <h2>{{ $t('tenantMember.title') }}</h2>
          <t-popup placement="bottom-start" trigger="hover" overlay-class-name="permissions-popup-overlay"
            :overlay-inner-style="permissionsPopupInnerStyle">
            <button type="button" class="permissions-trigger-btn" :aria-label="$t('tenantMember.permissions.title')"
              :title="$t('tenantMember.permissions.iconHint')">
              <t-icon name="info-circle" size="16px" />
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
                      <span v-for="(perm, i) in roleMatrix[r]" :key="i" :class="['perm-item', perm.has ? 'has' : 'no']">
                        <t-icon :name="perm.has ? 'check' : 'close'" size="12px" />
                        {{ $t('tenantMember.permissions.' + perm.key) }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </template>
          </t-popup>
          <!-- Audit log entry sits inline with the title: title (i)
               [审计日志]. Keeping all section-level affordances on the
               left edge avoids the "lonely right-aligned button"
               pattern in narrow settings panels. -->
          <t-button v-if="canViewAudit" variant="text" size="small" class="header-audit-btn" @click="openAuditDrawer">
            <template #icon><t-icon name="history" /></template>
            {{ $t('tenantMember.audit.tabLabel') }}
          </t-button>
        </div>
      </div>
      <p class="section-description">{{ $t('tenantMember.sectionDescription') }}</p>
    </div>

    <div class="members-tab-layout">
      <!-- Toolbar 已被并入「空间成员」列表头：搜索框紧贴列表头右
           侧，邀请按钮再往右一个图标位，所有「针对这张列表」的控
           件聚到同一行，独立 toolbar 不复存在。 -->

      <!-- Pending invitations. Shown only to managers because the
               viewer/contributor roles don't have an action surface
               here. Even when empty we still render the header so
               operators get a stable "is there anything pending?"
               affordance after they hit "Send invitation". -->
      <div v-if="canManage" class="pending-invitations-section">
        <div class="pending-invitations-header">
          <div class="pending-invitations-titlewrap">
            <span class="pending-invitations-title">
              {{ $t('tenantInvitation.pendingSectionTitle') }}
            </span>
            <!-- Same count-badge style as the «空间成员» list header
                 so the two list titles read at parity. -->
            <span class="members-list-count-badge">{{ invitationsTotal }}</span>
          </div>
          <span class="pending-invitations-desc">
            {{ $t('tenantInvitation.pendingSectionDesc', { days: INVITATION_TTL_DAYS }) }}
          </span>
        </div>
        <div v-if="invitationsLoading" class="loading-inline">
          <t-loading size="small" />
          <span>{{ $t('tenantMember.loading') }}</span>
        </div>
        <div v-else-if="invitationsError" class="error-inline">
          <t-alert theme="error" :message="invitationsError">
            <template #operation>
              <t-button size="small" @click="loadInvitations">{{ $t('tenantMember.retry') }}</t-button>
            </template>
          </t-alert>
        </div>
        <div v-else-if="invitationsTotal === 0" class="pending-invitations-empty">
          {{ $t('tenantInvitation.pendingEmpty') }}
        </div>
        <div v-else class="data-table-shell data-table-shell--with-footer pending-invitations-table">
          <div class="data-table-shell__scroll">
            <t-table row-key="id" :data="invitations" :columns="invitationColumns" size="medium" hover>
              <template #invitee="{ row }">
                <div class="member-cell">
                  <span class="member-name">{{ inviteePrimary(row) }}</span>
                  <span v-if="row.invitee_email && row.invitee_name" class="member-email">{{ row.invitee_email
                    }}</span>
                </div>
              </template>
              <template #role="{ row }">
                <t-tag :theme="roleTagTheme(row.role)" size="small">
                  {{ $t('tenantMember.role.' + row.role) }}
                </t-tag>
              </template>
              <template #inviter="{ row }">
                <span>{{ inviterPrimary(row) }}</span>
              </template>
              <template #expires_at="{ row }">{{ formatDate(row.expires_at) }}</template>
              <template #status="{ row }">
                <t-tag :theme="invitationStatusTheme(row.status)" size="small">
                  {{ $t('tenantInvitation.status.' + row.status) }}
                </t-tag>
              </template>
              <template #actions="{ row }">
                <!-- Inline popconfirm anchored to the revoke button.
                       Avoids spawning a top-level modal for a simple
                       yes/no decision; the popover stays inside the
                       table cell so the user keeps spatial context. -->
                <t-popconfirm v-if="row.status === 'pending'" theme="warning" :content="$t('tenantInvitation.revoke.confirmBody', {
                  email: row.invitee_email || row.invitee_user_id,
                })" :confirm-btn="{ content: $t('tenantInvitation.revoke.confirm'), theme: 'danger' }"
                  :cancel-btn="$t('common.cancel')" placement="left" @confirm="doRevokeInvitation(row)">
                  <t-button theme="danger" variant="text" size="small">
                    {{ $t('tenantInvitation.revoke.button') }}
                  </t-button>
                </t-popconfirm>
              </template>
            </t-table>
          </div>
          <div v-if="invitationsTotal > 0" class="data-table-shell__pager">
            <t-pagination v-model="invitationsPage" v-model:page-size="invitationsPageSize" :total="invitationsTotal"
              size="small" show-jumper show-page-number show-page-size
              :page-size-options="INVITATIONS_PAGE_SIZE_OPTIONS" @change="onInvitationsPageChange" />
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

      <!-- 无成员或当前筛选条件下无命中 -->
      <div v-else-if="membersTotal === 0" class="empty-state">
        <t-empty :description="searchQuery.trim()
          ? $t('tenantMember.emptySearch', { q: searchQuery })
          : $t('tenantMember.empty')
          " />
      </div>

      <!-- Member table. The list is preceded by a small section title
           («成员 N  +») that mirrors the "Pending invitations" header
           above it. The trailing «+» icon button replaces the toolbar
           "Invite member" CTA — the action belongs next to the list it
           mutates, not in a separate band. -->
      <div v-else class="members-list-wrap">
        <div class="members-list-header">
          <div class="members-list-titlewrap">
            <span class="members-list-title">{{ $t('tenantMember.listTitle') }}</span>
            <span class="members-list-count-badge">{{ membersTotal }}</span>
          </div>
          <div class="members-list-actions">
            <div class="members-list-search">
              <t-input v-model="searchQuery" size="small" :placeholder="$t('tenantMember.searchPlaceholder')" clearable>
                <template #prefix-icon><t-icon name="search" /></template>
              </t-input>
            </div>
            <t-popup v-if="canManage" v-model="invitePopupVisible" trigger="click" placement="bottom-end"
              destroy-on-close overlay-class-name="member-invite-popup-overlay">
              <t-button theme="primary" variant="outline" shape="square" size="small" class="members-list-add-btn"
                :title="$t('tenantMember.add.button')" :aria-label="$t('tenantMember.add.button')">
                <template #icon><t-icon name="user-add" /></template>
              </t-button>
              <template #content>
                <div class="member-invite-popup-inner" @click.stop>
                  <div class="member-invite-popup-title">
                    {{
                      addDialogStep === 'form'
                        ? $t('tenantMember.add.dialogTitle')
                        : $t('tenantInvitation.confirmInviteTitle')
                    }}
                  </div>
                  <t-form v-if="addDialogStep === 'form'" ref="addFormRef" :data="addForm" :rules="addFormRules"
                    :label-width="80" class="member-invite-form">
                    <t-form-item :label="$t('tenantMember.add.emailLabel')" name="email">
                      <t-input v-model="addForm.email" :placeholder="$t('tenantMember.add.emailPlaceholder')"
                        clearable />
                    </t-form-item>
                    <t-form-item :label="$t('tenantMember.add.roleLabel')" name="role">
                      <t-select v-model="addForm.role" :options="roleOptions" :popup-props="roleSelectPopupProps" />
                    </t-form-item>
                  </t-form>
                  <div v-else class="invite-confirm-body">
                    {{ $t('tenantInvitation.confirmInviteBody', {
                      email: addConfirmEmail,
                      role: addConfirmRoleLabel,
                    }) }}
                  </div>
                  <div class="invite-popup-footer">
                    <t-button v-if="addDialogStep === 'form'" variant="outline" :disabled="adding"
                      @click="invitePopupVisible = false">
                      {{ $t('common.cancel') }}
                    </t-button>
                    <t-button v-else variant="outline" :disabled="adding" @click="goBackToForm">
                      {{ $t('common.back') }}
                    </t-button>
                    <t-button theme="primary" :loading="adding" @click="submitAdd">
                      {{ dialogConfirmLabel }}
                    </t-button>
                  </div>
                </div>
              </template>
            </t-popup>
          </div>
        </div>
        <div class="data-table-shell data-table-shell--with-footer">
          <div class="data-table-shell__scroll">
            <t-table row-key="user_id" :data="members" :columns="columns" size="medium" hover stripe>
              <template #member="{ row }">
                <div class="member-cell">
                  <span class="member-name">{{ memberPrimary(row) }}</span>
                  <span v-if="memberSecondary(row)" class="member-email">{{ memberSecondary(row) }}</span>
                </div>
              </template>
              <template #role="{ row }">
                <div class="role-cell">
                  <t-select v-if="canManage && row.user_id !== currentUserId" :model-value="row.role"
                    class="member-role-select" :options="roleOptions" size="small" :popup-props="roleSelectPopupProps"
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
          <div v-if="membersTotal > 0" class="data-table-shell__pager">
            <t-pagination v-model="membersPage" v-model:page-size="membersPageSize" :total="membersTotal" size="small"
              show-jumper show-page-number show-page-size :page-size-options="MEMBERS_PAGE_SIZE_OPTIONS"
              @change="onMembersPageChange" />
          </div>
        </div>
      </div>

    </div>

    <!-- Audit log drawer. Only rendered for Admin+ because the backend
         route is g.Admin()-gated; rendering it for lower roles would
         just produce an unhelpful 403. Lazy-loaded on first open. -->
    <t-drawer v-if="canViewAudit" v-model:visible="auditDrawerVisible" :header="$t('tenantMember.audit.tabLabel')"
      drawer-class-name="tenant-members-audit-drawer" size="720px" :footer="false" placement="right" destroy-on-close>
      <div class="audit-drawer-inner audit-panel audit-panel--drawer">
        <div class="audit-header">
          <span class="audit-desc">{{ $t('tenantMember.audit.description') }}</span>
          <t-button theme="primary" variant="outline" shape="square" size="small" class="audit-refresh-btn"
            :loading="auditLoading" :title="$t('tenantMember.audit.refresh')"
            :aria-label="$t('tenantMember.audit.refresh')" @click="reloadAuditLog">
            <template #icon><t-icon name="refresh" /></template>
          </t-button>
        </div>

        <div class="audit-drawer-fill">
          <div v-if="auditError" class="audit-drawer-branch audit-drawer-branch--error">
            <div class="error-inline">
              <t-alert theme="error" :message="auditError">
                <template #operation>
                  <t-button size="small" @click="reloadAuditLog">
                    {{ $t('tenantMember.retry') }}
                  </t-button>
                </template>
              </t-alert>
            </div>
          </div>

          <div v-else-if="!auditLoading && auditEntries.length === 0"
            class="audit-drawer-branch audit-drawer-branch--empty empty-state empty-state--audit">
            <t-empty :description="$t('tenantMember.audit.empty')" />
          </div>

          <div v-else class="audit-scroll-area narrow-scrollbar audit-drawer-branch" ref="auditScrollRoot">
            <div class="data-table-shell">
              <t-table row-key="id" :data="auditEntries" :columns="auditColumns" size="medium" hover stripe>
                <template #created_at="{ row }">{{ formatDate(row.created_at) }}</template>
                <template #actor="{ row }">
                  <span class="audit-actor">
                    {{ row.actor_user_id ? actorDisplayName(row.actor_user_id) :
                      $t('tenantMember.audit.systemActor') }}
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

            <!-- 触底 sentinel：IntersectionObserver root 指向 audit-scroll-area -->
            <div ref="auditLoadSentinelEl" class="audit-load-sentinel" aria-hidden="true" />

            <div v-if="auditLoading && auditEntries.length > 0" class="audit-loading-more">
              <t-loading size="small" />
              <span>{{ $t('tenantMember.loading') }}</span>
            </div>

            <p v-if="!auditHasMore && auditEntries.length > 0 && !auditLoading" class="audit-end-hint">
              {{ $t('tenantMember.audit.end') }}
            </p>
          </div>
        </div>
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listMembers,
  updateMemberRole,
  removeMember,
  type TenantMember,
  type TenantRole,
} from '@/api/tenant/members'
import {
  listTenantInvitations,
  createInvitation,
  revokeInvitation,
  type TenantInvitation,
} from '@/api/tenant/invitations'
import {
  listAuditLog,
  type AuditLog,
  type AuditAction,
  type AuditOutcome,
} from '@/api/tenant/audit-log'

const { t, tm, locale } = useI18n()
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
/** 邀请流程：锚在列表头「+」按钮旁的弹出层（非居中模态）。 */
const invitePopupVisible = ref(false)
// Two-step invite inside the popup: 'form' renders the email/role inputs;
// 'confirm' swaps the body for an in-place summary; primary CTA toggles label.
const addDialogStep = ref<'form' | 'confirm'>('form')
const addFormRef = ref<any>(null)
const searchQuery = ref('')
/** 已应用到服务端筛选的检索词（相对输入框防抖） */
const memberSearchQ = ref('')
let memberSearchDebounceTimer: number | undefined

const membersTotal = ref(0)
const membersPage = ref(1)
const membersPageSize = ref(20)

const invitationsTotal = ref(0)
const invitationsPage = ref(1)
const invitationsPageSize = ref(20)

/** 历次分页载荷里见过的成员展示字段，补齐审计表里不在当前页的 user id */
const memberDisplayByUserId = reactive<Record<string, { username?: string; email?: string }>>({})

// Pending invitations live alongside members but in a distinct section
// at the top of the Members tab — they're "people we've asked to
// join but haven't yet accepted", and conflating them with the
// authoritative roster would mislead an Owner trying to see who
// actually has access. The load happens on the same trigger as the
// members fetch so the screen renders both at once.
const invitations = ref<TenantInvitation[]>([])
const invitationsLoading = ref(false)
const invitationsError = ref('')
// Invitation TTL is mirrored from the backend constant
// (defaultInvitationTTL in tenant_invitation.go). Kept as a UI string
// for the section description; the authoritative number comes from
// the server's expires_at on each row.
const INVITATION_TTL_DAYS = 7

const MEMBERS_PAGE_SIZE_OPTIONS = [10, 20, 50, 100]
const INVITATIONS_PAGE_SIZE_OPTIONS = [10, 20, 50, 100]

// Audit log moved out of t-tabs into a right-side drawer; this flag
// controls its visibility. Default closed — most operators come here
// to manage members, not to audit. Lazy-load logic is wired through
// openAuditDrawer() rather than a watch() on the visibility flag so a
// re-open of the drawer doesn't re-trigger a fetch the user didn't ask
// for (they have an explicit "refresh" button inside the drawer).
const auditDrawerVisible = ref(false)

// Audit-log state. Backend cursor-paged by descending id (`after_id`);
// frontend appends rows when the sentinel scrolls into view. When
// `next_cursor` is 0, `auditHasMore` becomes false and loading stops.
const auditEntries = ref<AuditLog[]>([])
const auditLoading = ref(false)
const auditError = ref('')
const auditCursor = ref<number>(0) // 0 = "from the top"
const auditHasMore = ref(true)
const auditLoadedOnce = ref(false)
const AUDIT_PAGE_SIZE = 50

/** 抽屉内滚动根与触底 sentinel，用于游标分页自动加载下一页（见 attachAuditInfiniteScroll） */
const auditScrollRoot = ref<HTMLElement | null>(null)
const auditLoadSentinelEl = ref<HTMLElement | null>(null)
let auditScrollObserver: IntersectionObserver | null = null

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

/** 下拉层须高于邀请浮层（3050）与组织设置全屏遮罩，否则会被压住 */
const roleSelectPopupProps = {
  zIndex: 6200,
  overlayClassName: 'tenant-members-role-select-popup',
}

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
      return 'user-vip-filled'
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
  { colKey: 'role', title: t('tenantMember.columns.role'), width: 112 },
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

function rememberMembersForAudit(rows: TenantMember[]) {
  for (const m of rows) {
    memberDisplayByUserId[m.user_id] = { username: m.username, email: m.email }
  }
}

async function loadMembers() {
  if (!activeTenantId.value) {
    return
  }
  loading.value = true
  error.value = ''
  try {
    const resp = await listMembers(activeTenantId.value, {
      page: membersPage.value,
      page_size: membersPageSize.value,
      q: memberSearchQ.value || undefined,
    })
    if (resp.success && resp.data) {
      const total = resp.data.total ?? 0
      const ps = resp.data.page_size ?? membersPageSize.value
      const safePs = Math.max(1, ps)
      const maxPage = Math.max(1, Math.ceil(total / safePs))
      if (membersPage.value > maxPage) {
        membersPage.value = maxPage
        loading.value = false
        await loadMembers()
        return
      }
      members.value = resp.data.members ?? []
      membersTotal.value = total
      if (typeof resp.data.page === 'number' && resp.data.page > 0) {
        membersPage.value = resp.data.page
      }
      if (typeof resp.data.page_size === 'number' && resp.data.page_size > 0) {
        membersPageSize.value = resp.data.page_size
      }
      rememberMembersForAudit(members.value)
    } else {
      error.value = resp.message || t('tenantMember.errors.generic')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenantMember.errors.generic')
  } finally {
    loading.value = false
  }
}

function onMembersPageChange() {
  void loadMembers()
}

watch(searchQuery, () => {
  if (!activeTenantId.value) return
  window.clearTimeout(memberSearchDebounceTimer)
  memberSearchDebounceTimer = window.setTimeout(() => {
    memberSearchQ.value = searchQuery.value.trim()
    membersPage.value = 1
    loadMembers()
  }, 320)
})

// ---- Pending invitations ------------------------------------------------

const invitationColumns = computed(() => [
  { colKey: 'invitee', title: t('tenantInvitation.columns.invitee'), ellipsis: true, minWidth: 160 },
  { colKey: 'role', title: t('tenantInvitation.columns.role'), width: 110 },
  { colKey: 'inviter', title: t('tenantInvitation.columns.inviter'), ellipsis: true, minWidth: 140 },
  { colKey: 'expires_at', title: t('tenantInvitation.columns.expiresAt'), width: 160 },
  { colKey: 'status', title: t('tenantInvitation.columns.status'), width: 100 },
  ...(canManage.value
    ? [{ colKey: 'actions', title: t('tenantInvitation.columns.operations'), width: 88, align: 'right' as const }]
    : []),
])

function invitationStatusTheme(s: TenantInvitation['status']): 'primary' | 'success' | 'warning' | 'danger' | 'default' {
  switch (s) {
    case 'pending':
      return 'primary'
    case 'accepted':
      return 'success'
    case 'declined':
    case 'revoked':
      return 'warning'
    case 'expired':
      return 'danger'
    default:
      return 'default'
  }
}

function inviteePrimary(row: TenantInvitation): string {
  return row.invitee_name?.trim() || row.invitee_email?.trim() || row.invitee_user_id
}

function inviterPrimary(row: TenantInvitation): string {
  return row.inviter_name?.trim() || row.inviter_email?.trim() || row.invited_by || '—'
}

// loadInvitations is called from the same trigger as loadMembers so
// the Members tab can render the pending section without an extra
// round-trip latency budget. canManage gates the fetch — viewers /
// contributors / admins-without-management see no pending section at
// all (the route returns 200 but listing pending invites to those
// roles would be UX noise; the route layer is Viewer+ for the read
// itself).
async function loadInvitations() {
  if (!activeTenantId.value || !canManage.value) {
    invitations.value = []
    invitationsTotal.value = 0
    return
  }
  invitationsLoading.value = true
  invitationsError.value = ''
  try {
    const resp = await listTenantInvitations(activeTenantId.value, {
      page: invitationsPage.value,
      page_size: invitationsPageSize.value,
    })
    if (resp.success && resp.data) {
      const total = resp.data.total ?? 0
      const ps = resp.data.page_size ?? invitationsPageSize.value
      const safePs = Math.max(1, ps)
      const maxPage = Math.max(1, Math.ceil(total / safePs))
      if (invitationsPage.value > maxPage) {
        invitationsPage.value = maxPage
        invitationsLoading.value = false
        await loadInvitations()
        return
      }
      invitations.value = resp.data.invitations ?? []
      invitationsTotal.value = total
      if (typeof resp.data.page === 'number' && resp.data.page > 0) {
        invitationsPage.value = resp.data.page
      }
      if (typeof resp.data.page_size === 'number' && resp.data.page_size > 0) {
        invitationsPageSize.value = resp.data.page_size
      }
    } else {
      invitationsError.value = resp.message || t('tenantInvitation.errors.generic')
    }
  } catch (err: any) {
    invitationsError.value = err?.message || t('tenantInvitation.errors.generic')
  } finally {
    invitationsLoading.value = false
  }
}

function onInvitationsPageChange() {
  void loadInvitations()
}

// doRevokeInvitation is wired to the t-popconfirm @confirm event in
// the table template. The popconfirm itself owns the yes/no surface,
// so this function is the post-confirmation action only — no nested
// modal. Errors surface as toasts and the row stays in place for retry.
async function doRevokeInvitation(row: TenantInvitation) {
  try {
    const resp = await revokeInvitation(activeTenantId.value, row.id)
    if (resp.success) {
      await loadInvitations()
      MessagePlugin.success(t('tenantInvitation.revoke.success'))
    } else {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 404) {
      MessagePlugin.error(t('tenantInvitation.errors.notFound'))
    } else if (status === 409) {
      MessagePlugin.error(err?.message || t('tenantInvitation.errors.notPending'))
    } else {
      MessagePlugin.error(err?.message || t('tenantInvitation.errors.generic'))
    }
  }
}

// ---- Audit-log helpers --------------------------------------------------

/** ellipsis 自带的 Tooltip 默认主题在深色抽屉语境下易产生暗底黑字 */
const auditTableEllipsisTooltip = { theme: 'light' as const }

const auditColumns = computed(() => [
  { colKey: 'created_at', title: t('tenantMember.audit.columns.time'), width: 150 },
  {
    colKey: 'actor',
    title: t('tenantMember.audit.columns.actor'),
    minWidth: 140,
    ellipsis: auditTableEllipsisTooltip,
  },
  { colKey: 'action', title: t('tenantMember.audit.columns.action'), width: 130 },
  {
    colKey: 'target',
    title: t('tenantMember.audit.columns.target'),
    minWidth: 140,
    ellipsis: auditTableEllipsisTooltip,
  },
  {
    colKey: 'request_path',
    title: t('tenantMember.audit.columns.path'),
    minWidth: 140,
    ellipsis: auditTableEllipsisTooltip,
  },
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

// i18n 键名含点号（rbac.member_added）。用 t(path) 会按路径拆开解析，
// 无法命中 tenantMember.audit.action['rbac.*'] — 必须用 tm + 字面量键。
function formatAuditAction(action: AuditAction): string {
  const bag = tm('tenantMember.audit.action') as unknown
  if (bag !== null && typeof bag === 'object' && typeof (bag as Record<string, string>)[action] === 'string') {
    return (bag as Record<string, string>)[action]
  }
  return action
}

// Resolve a user id to a display label: prefer current页的 members，
// 再退到历次分页积累的 memberDisplayByUserId，最后是原始 id。
function actorDisplayName(userId: string): string {
  const cur = members.value.find((x) => x.user_id === userId)
  if (cur?.username?.trim()) return cur.username.trim()
  if (cur?.email?.trim()) return cur.email.trim()
  const memo = memberDisplayByUserId[userId]
  if (memo?.username?.trim()) return memo.username!.trim()
  if (memo?.email?.trim()) return memo.email!.trim()
  return userId
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

function detachAuditInfiniteScroll() {
  auditScrollObserver?.disconnect()
  auditScrollObserver = null
}

function attachAuditInfiniteScroll() {
  detachAuditInfiniteScroll()
  const root = auditScrollRoot.value
  const sentinel = auditLoadSentinelEl.value
  if (!root || !sentinel) return

  auditScrollObserver = new IntersectionObserver(
    (entries) => {
      const hitBottom = entries.some((e) => e.isIntersecting)
      if (!hitBottom || !auditHasMore.value || auditLoading.value) return
      void loadAuditLog(false)
    },
    { root, rootMargin: '100px 0px', threshold: 0 },
  )
  auditScrollObserver.observe(sentinel)
}

function reloadAuditLog() {
  auditCursor.value = 0
  auditHasMore.value = true
  loadAuditLog(true)
}

// Lazy-load the audit log the first time the drawer is opened. We
// avoid watching `auditDrawerVisible` so closing+reopening the drawer
// doesn't re-fetch behind the user's back — refresh is an explicit
// action via the drawer's "Refresh" button.
function openAuditDrawer() {
  auditDrawerVisible.value = true
  if (!auditLoadedOnce.value) {
    loadAuditLog(true)
  }
}

watch(
  auditDrawerVisible,
  async (open) => {
    if (!open) {
      detachAuditInfiniteScroll()
      return
    }
    await nextTick()
    attachAuditInfiniteScroll()
  },
  { flush: 'post' },
)

watch(
  () => auditError.value,
  async () => {
    if (!auditDrawerVisible.value) return
    await nextTick()
    if (!auditError.value) {
      attachAuditInfiniteScroll()
      return
    }
    detachAuditInfiniteScroll()
  },
  { flush: 'post' },
)

onUnmounted(() => detachAuditInfiniteScroll())

watch(invitePopupVisible, (open) => {
  if (!open) return
  addForm.email = ''
  addForm.role = 'contributor'
  addDialogStep.value = 'form'
})

// Live display strings for the in-place confirm step. Recomputed
// every time the user goes Back, tweaks the form, and re-advances —
// the summary always mirrors the current form state.
const addConfirmEmail = computed(() => addForm.email.trim())
const addConfirmRoleLabel = computed(() => t('tenantMember.role.' + addForm.role))

// submitAdd is wired to the popup footer primary CTA. On step='form' it
// validates and swaps to summary; on step='confirm' it fires the API.
async function submitAdd() {
  if (addDialogStep.value === 'form') {
    const valid = await addFormRef.value?.validate?.()
    if (valid !== true) return
    addDialogStep.value = 'confirm'
    return
  }
  await sendInvitation(addForm.email.trim(), addForm.role)
}

// goBackToForm un-advances from confirm to form inside the popup.
function goBackToForm() {
  addDialogStep.value = 'form'
}

// dialogConfirmLabel drives the primary action label across the two steps.
const dialogConfirmLabel = computed(() =>
  addDialogStep.value === 'form'
    ? t('tenantInvitation.inviteSubmit')
    : t('tenantInvitation.confirmSend'),
)

// sendInvitation actually fires the create-invitation API call.
async function sendInvitation(email: string, role: TenantRole) {
  adding.value = true
  try {
    const resp = await createInvitation(activeTenantId.value, { email, role })
    if (resp.success) {
      invitationsPage.value = 1
      await loadInvitations()
      invitePopupVisible.value = false
      MessagePlugin.success(t('tenantInvitation.inviteSuccess'))
    } else {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 404) {
      MessagePlugin.error(t('tenantMember.errors.userNotFound'))
    } else if (status === 409) {
      // Server returns the same 409 for both "already a member" and
      // "already a pending invite". The message body discriminates,
      // but for the toast we show both possibilities folded into one
      // helpful line.
      MessagePlugin.error(
        err?.message ||
        `${t('tenantInvitation.errors.alreadyMember')} / ${t(
          'tenantInvitation.errors.pendingExists',
        )}`,
      )
    } else if (status === 400) {
      MessagePlugin.error(err?.message || t('tenantMember.errors.invalidRole'))
    } else {
      MessagePlugin.error(err?.message || t('tenantInvitation.errors.generic'))
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
      // Mutate the row by replacing it in `members.value` instead of
      // assigning `row.role = next` in place. The `row` argument here
      // is the row object handed in by t-table's slot scope, which in
      // some TDesign versions is a shallow copy that doesn't share
      // reactivity with the `members` array — assigning `row.role`
      // updates the local handle but not the rendered cell, so the
      // select keeps showing the previous value until a refresh.
      // Splicing a fresh object into the source array guarantees the
      // table re-renders.
      const idx = members.value.findIndex((m) => m.user_id === row.user_id)
      if (idx >= 0) {
        const merged = { ...members.value[idx], role: next }
        members.value.splice(idx, 1, merged)
        rememberMembersForAudit([merged])
      } else {
        row.role = next
      }
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
          await loadMembers()
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
    if (id) {
      searchQuery.value = ''
      memberSearchQ.value = ''
      window.clearTimeout(memberSearchDebounceTimer)
      membersPage.value = 1
      invitationsPage.value = 1
      membersPageSize.value = 20
      invitationsPageSize.value = 20
      membersTotal.value = 0
      invitationsTotal.value = 0
      loadMembers()
      loadInvitations()
    }
  },
  { immediate: true },
)
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
    margin: 0;
    letter-spacing: -0.02em;
  }

  .section-description {
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.55;
    margin: 6px 0 0 0;
    max-width: 52rem;
  }
}

.section-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.section-header-titlewrap {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

/* 顶部右侧的「审计日志」入口。t-button variant="text" 自带颜色
   交互；这里只调 flex 行为，避免在 wrap 时被挤压。 */
.header-audit-btn {
  flex-shrink: 0;
}

.members-tab-layout {
  display: flex;
  flex-direction: column;
}

/* 带子分页的卡片：仅在表格主体上横向滚动，页脚不参与滚动，避免分页条被卷入或对齐错位。
   双类选择器用于盖过根上 .data-table-shell 的 overflow-x: auto */
.data-table-shell.data-table-shell--with-footer {
  display: flex;
  flex-direction: column;
  overflow: hidden;

  >.data-table-shell__scroll {
    overflow-x: auto;
    min-width: 0;
  }

  >.data-table-shell__pager {
    flex-shrink: 0;
    display: flex;
    justify-content: flex-end;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px 12px;
    padding: 10px 14px;
    border-top: 1px solid var(--td-component-stroke);
    background-color: var(--td-bg-color-container);

    :deep(.t-pagination) {
      flex-wrap: wrap;
      justify-content: flex-end;
      row-gap: 8px;
    }
  }
}

/* 待接受区块整体为浅底色，分页条与表格区同色阶、仅靠顶部分割线与表格区分 */
.pending-invitations-table.data-table-shell.data-table-shell--with-footer>.data-table-shell__pager {
  background-color: transparent;
}

.permissions-trigger-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 6px;
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

/* 列表上方的标题行：「空间成员 [N] · 筛选出 K」 左侧；右侧
   是「搜索 + 邀请按钮」一组。视觉级别与「待接受邀请」一致。 */
.members-list-wrap {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.members-list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 2px;
  flex-wrap: wrap;
}

.members-list-titlewrap {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.members-list-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

/* 数字外面套一个浅底圆角徽章，避免裸露的「成员 1」读起来像
   排版残留。色阶与 td-tag default+light 看齐。 */
.members-list-count-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 22px;
  height: 20px;
  padding: 0 7px;
  border-radius: 10px;
  background-color: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
}

.members-list-filter-hint {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.members-list-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: 0 1 auto;
  min-width: 0;
}

.members-list-search {
  flex: 0 1 14rem;
  min-width: 8rem;
  max-width: 16rem;

  :deep(.t-input) {
    width: 100%;
  }
}

/* 列表头的邀请按钮：outline + primary，自带外框，比裸 icon
   有分量；shape="square" 让它仍然是个紧凑的图标按钮。 */
.members-list-add-btn {
  flex-shrink: 0;
}

@media (max-width: 560px) {
  .members-list-actions {
    width: 100%;
    justify-content: flex-start;
  }

  .members-list-search {
    flex: 1 1 auto;
    max-width: none;
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

  /* 角色列：下拉收缩到内容宽度，不再撑满整格。原先 100% 在窄角色
     名（如"Owner"）下显得空荡且与其他列对不齐。 */
  &:deep(.role-cell) {
    display: flex;
    align-items: center;
    min-width: 0;
    box-sizing: border-box;
  }

  &:deep(.member-role-select.t-select),
  &:deep(.member-role-select.t-select > .t-select__wrap) {
    flex: 0 0 auto;
    width: 92px;
    min-width: 0;
    box-sizing: border-box;
  }

  /* 收紧 select 内部 padding，让最长角色名 "Contributor" 也能在
     92px 内显示而不被中点截掉。 */
  &:deep(.member-role-select .t-input) {
    padding-left: 8px;
    padding-right: 4px;
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

/* Invite popup: confirm copy + footer actions (anchored beside +). */
.member-invite-popup-inner {
  max-width: 100%;
}

.member-invite-popup-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 12px;
  line-height: 1.35;
}

.member-invite-form {
  &:deep(.t-form__item) {
    margin-bottom: 14px;

    &:last-child {
      margin-bottom: 4px;
    }
  }
}

.invite-confirm-body {
  padding: 4px 0 8px;
  color: var(--td-text-color-primary);
  font-size: 14px;
  line-height: 1.6;
}

.invite-popup-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}

.pending-invitations-section {
  margin-bottom: 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;

  .pending-invitations-header {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .pending-invitations-titlewrap {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .pending-invitations-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .pending-invitations-desc {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  .pending-invitations-empty {
    padding: 10px 12px;
    border: 1px dashed var(--td-component-stroke);
    border-radius: 8px;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    background: var(--td-bg-color-container);
  }

  /* Visually distinguish from members table so the eye doesn't fuse
     "pending" rows with "actual member" rows. */
  .pending-invitations-table {
    background: var(--td-bg-color-secondarycontainer);
  }
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

/* Inside the drawer the t-drawer body already gives us padding, so
   drop the top offset; otherwise the audit-header floats away from
   the drawer title with no visual anchor. */
.audit-panel--drawer {
  padding-top: 0;
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
    flex: 1;
    min-width: 0;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .audit-refresh-btn {
    flex-shrink: 0;
  }
}

.audit-drawer-inner {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  gap: 14px;
  min-height: 0;
  width: 100%;
  box-sizing: border-box;
}

.audit-drawer-fill {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-drawer-branch {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.audit-drawer-branch--error {
  justify-content: center;

  .error-inline {
    width: 100%;
  }
}

.audit-drawer-branch--empty.empty-state--audit {
  flex: 1 1 auto;
  justify-content: center;
  align-items: center;
  padding: 24px 12px;
  min-height: 0;
}

.audit-scroll-area {
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
}

.audit-load-sentinel {
  height: 1px;
  width: 100%;
  pointer-events: none;
}

.audit-loading-more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 12px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.audit-end-hint {
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-disabled);
  padding: 8px 0 14px;
  margin: 0;
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
</style>

<style lang="less">
/* t-popup 挂到 body，需全局样式；z-index 需高于设置全屏遮罩（2000）。 */
.member-invite-popup-overlay {
  z-index: 3050 !important;

  .t-popup__content {
    padding: 14px 16px !important;
    min-width: 300px;
    max-width: min(392px, calc(100vw - 24px));
    border-radius: 12px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
    backdrop-filter: blur(20px) saturate(180%) !important;
    -webkit-backdrop-filter: blur(20px) saturate(180%) !important;
  }
}

:root[theme-mode='dark'] .member-invite-popup-overlay .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 4px rgba(0, 0, 0, 0.12),
    0 8px 32px rgba(0, 0, 0, 0.28) !important;
}

/* 角色下拉挂到 body 时可能被邀请 Popup / 设置遮罩盖住，类名挂在 t-popup 根节点 */
.tenant-members-role-select-popup {
  z-index: 6200 !important;
}

/* 成员页审计抽屉 teleport 到 body，须全局样式才能把 body 高度链拉满以便内层滚动 */
.t-drawer.tenant-members-audit-drawer.t-drawer--right .t-drawer__content-wrapper--right {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  max-height: 100vh;
  height: 100%;
}

.t-drawer.tenant-members-audit-drawer .t-drawer__body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  overflow: hidden !important;
}
</style>
