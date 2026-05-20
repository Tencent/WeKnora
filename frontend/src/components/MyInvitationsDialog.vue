<template>
  <!-- "My invitations" inbox as a dialog. The previous version of
       this was a full /platform/invitations route, but the inbox is
       intrinsically transient — open it, act, dismiss — and a modal
       keeps the user in whatever context the bell icon was clicked
       from rather than yanking them to a separate page. The trigger
       lives in UserMenu's avatar-row bell. -->
  <t-dialog v-model:visible="visibleModel" :header="$t('tenantInvitation.myInbox.title')" :footer="false" width="540px"
    :close-on-overlay-click="true">
    <p class="my-invitations-desc">{{ $t('tenantInvitation.myInbox.description') }}</p>

    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('tenantMember.loading') }}</span>
    </div>

    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="reload">{{ $t('tenantMember.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <div v-else-if="invitations.length === 0" class="empty-state">
      <t-empty :description="$t('tenantInvitation.myInbox.empty')" />
    </div>

    <ul v-else class="invitation-list">
      <li v-for="row in invitations" :key="row.id" class="invitation-card">
        <div class="invitation-card-main">
          <div class="invitation-card-header">
            <span class="tenant-name">
              {{ row.tenant_name || $t('tenantInvitation.myInbox.tenantLabel') + ' #' + row.tenant_id }}
            </span>
            <t-tag :theme="roleTagTheme(row.role)" size="small">
              {{ $t('tenantMember.role.' + row.role) }}
            </t-tag>
          </div>
          <div class="invitation-card-meta">
            <span class="meta-row">
              <t-icon name="user" size="14px" class="meta-icon" />
              <span class="meta-label">{{ $t('tenantInvitation.myInbox.from') }}：</span>
              <span class="meta-value">{{ inviterDisplay(row) }}</span>
            </span>
            <span class="meta-row">
              <t-icon name="time" size="14px" class="meta-icon" />
              <span class="meta-value">
                {{ $t('tenantInvitation.myInbox.expiresIn', { date: formatDate(row.expires_at) }) }}
              </span>
            </span>
            <span v-if="row.message" class="meta-row meta-row--message">
              <t-icon name="chat" size="14px" class="meta-icon" />
              <span class="meta-label">{{ $t('tenantInvitation.myInbox.messageLabel') }}：</span>
              <span class="meta-value">{{ row.message }}</span>
            </span>
          </div>
        </div>
        <div class="invitation-card-actions">
          <t-button theme="primary" size="small" :loading="acting === row.id" @click="onAccept(row)">
            {{ $t('tenantInvitation.myInbox.acceptButton') }}
          </t-button>
          <t-button theme="default" variant="outline" size="small" :loading="acting === row.id" @click="onDecline(row)">
            {{ $t('tenantInvitation.myInbox.declineButton') }}
          </t-button>
        </div>
      </li>
    </ul>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listMyInvitations,
  acceptInvitation,
  declineInvitation,
  type TenantInvitation,
} from '@/api/tenant/invitations'
import type { TenantRole } from '@/api/tenant/members'

// v-model:visible — the parent (UserMenu) owns the open/close state
// so the bell icon stays the single source of truth. Reload is run
// on every open transition so the list reflects accept/decline
// actions taken in another tab while the dialog was closed.
const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()
const visibleModel = computed({
  get: () => props.visible,
  set: (v) => emit('update:visible', v),
})

const { t, locale } = useI18n()
const authStore = useAuthStore()

const invitations = ref<TenantInvitation[]>([])
const loading = ref(false)
const error = ref('')
const acting = ref<number | null>(null)

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

function inviterDisplay(row: TenantInvitation): string {
  return row.inviter_name?.trim() || row.inviter_email?.trim() || row.invited_by || '—'
}

function formatDate(s: string): string {
  if (!s) return '-'
  try {
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(s))
  } catch {
    return s
  }
}

async function reload() {
  loading.value = true
  error.value = ''
  try {
    const resp = await listMyInvitations()
    if (resp.success && resp.data) {
      invitations.value = resp.data.invitations
      authStore.setPendingInvitationCount(
        invitations.value.filter((i) => i.status === 'pending').length,
      )
    } else {
      error.value = resp.message || t('tenantInvitation.errors.generic')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenantInvitation.errors.generic')
  } finally {
    loading.value = false
  }
}

async function onAccept(row: TenantInvitation) {
  acting.value = row.id
  try {
    const resp = await acceptInvitation(row.id)
    if (resp.success) {
      invitations.value = invitations.value.filter((x) => x.id !== row.id)
      authStore.setPendingInvitationCount(Math.max(0, authStore.pendingInvitationCount - 1))
      await authStore.refreshFromAuthMe()
      MessagePlugin.success(
        t('tenantInvitation.myInbox.acceptSuccess', {
          tenant: row.tenant_name || `#${row.tenant_id}`,
        }),
      )
    } else {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 404) MessagePlugin.error(t('tenantInvitation.errors.notFound'))
    else if (status === 403) MessagePlugin.error(t('tenantInvitation.errors.forbidden'))
    else if (status === 409) MessagePlugin.error(err?.message || t('tenantInvitation.errors.notPending'))
    else MessagePlugin.error(err?.message || t('tenantInvitation.errors.generic'))
  } finally {
    acting.value = null
  }
}

async function onDecline(row: TenantInvitation) {
  acting.value = row.id
  try {
    const resp = await declineInvitation(row.id)
    if (resp.success) {
      invitations.value = invitations.value.filter((x) => x.id !== row.id)
      authStore.setPendingInvitationCount(Math.max(0, authStore.pendingInvitationCount - 1))
      MessagePlugin.success(t('tenantInvitation.myInbox.declineSuccess'))
    } else {
      MessagePlugin.error(resp.message || t('tenantInvitation.errors.generic'))
    }
  } catch (err: any) {
    const status = err?.status
    if (status === 404) MessagePlugin.error(t('tenantInvitation.errors.notFound'))
    else if (status === 403) MessagePlugin.error(t('tenantInvitation.errors.forbidden'))
    else if (status === 409) MessagePlugin.error(err?.message || t('tenantInvitation.errors.notPending'))
    else MessagePlugin.error(err?.message || t('tenantInvitation.errors.generic'))
  } finally {
    acting.value = null
  }
}

// Refetch on every open. Costs one round-trip but the user clicked
// here specifically to see the latest state; serving stale data
// would only confuse the empty-state vs "1 invitation" cases.
watch(
  () => props.visible,
  (v) => {
    if (v) reload()
  },
)
</script>

<style lang="less" scoped>
.my-invitations-desc {
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.55;
  margin: 0 0 16px 0;
}

.loading-inline,
.error-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
}

.empty-state {
  padding: 16px 0 8px;
  display: flex;
  justify-content: center;
}

.invitation-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  /* Cap height so a user with many invitations can scroll within
     the dialog rather than the dialog growing past the viewport. */
  max-height: 60vh;
  overflow-y: auto;
}

.invitation-card {
  display: flex;
  align-items: stretch;
  gap: 12px;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;

  .invitation-card-main {
    flex: 1 1 auto;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .invitation-card-header {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;

    .tenant-name {
      font-size: 14px;
      font-weight: 600;
      color: var(--td-text-color-primary);
    }
  }

  .invitation-card-meta {
    display: flex;
    flex-direction: column;
    gap: 3px;
    color: var(--td-text-color-secondary);
    font-size: 12px;

    .meta-row {
      display: inline-flex;
      align-items: center;
      gap: 4px;

      .meta-icon {
        color: var(--td-text-color-placeholder);
        flex-shrink: 0;
      }

      .meta-value {
        color: var(--td-text-color-primary);
      }

      &--message {
        align-items: flex-start;

        .meta-icon {
          margin-top: 2px;
        }

        .meta-value {
          word-break: break-word;
        }
      }
    }
  }

  .invitation-card-actions {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    justify-content: center;
    gap: 6px;
    flex-shrink: 0;
  }
}
</style>
