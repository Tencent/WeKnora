<template>
  <div class="share-to-space-panel">
    <div class="share-panel-header">
      <div class="share-panel-header-row">
        <div class="share-panel-titlewrap">
          <h2 class="share-panel-title">{{ $t('organization.share.title') }}</h2>
          <t-popup placement="bottom-start" trigger="hover" overlay-class-name="share-hint-popup-overlay"
            :overlay-inner-style="shareHintPopupInnerStyle">
            <button type="button" class="share-hint-trigger-btn" :aria-label="$t('knowledgeEditor.share.hintTitle')"
              :title="$t('knowledgeEditor.share.hintTitle')">
              <t-icon name="info-circle" size="16px" />
            </button>
            <template #content>
              <div class="share-hint-popover">
                <p class="share-hint-title">{{ $t('knowledgeEditor.share.hintTitle') }}</p>
                <p class="share-hint-desc">{{ $t('knowledgeEditor.share.tip1') }}</p>
                <p class="share-hint-desc">{{ $t('knowledgeEditor.share.tip2') }}</p>
              </div>
            </template>
          </t-popup>
        </div>
      </div>
      <p class="share-panel-desc">{{ $t('knowledgeEditor.share.description') }}</p>
    </div>

    <div class="share-panel-list-wrap">
      <div class="share-panel-list-header">
        <div class="share-panel-titlewrap">
          <span class="share-panel-list-title">{{ $t('organization.share.sharedTo') }}</span>
          <span class="share-panel-count-badge">{{ filteredShares.length }}</span>
        </div>
        <div class="share-panel-actions">
          <div class="share-panel-search">
            <t-input v-model="searchQuery" size="small" :placeholder="$t('organization.share.searchPlaceholder')"
              clearable>
              <template #prefix-icon>
                <t-icon name="search" />
              </template>
            </t-input>
          </div>
          <t-popup v-if="canShare" v-model="addPopupVisible" trigger="click" placement="bottom-end" destroy-on-close
            overlay-class-name="share-add-popup-overlay">
            <t-button theme="primary" variant="outline" shape="square" size="small" class="share-panel-add-btn"
              :title="$t('knowledgeEditor.share.addShare')" :aria-label="$t('knowledgeEditor.share.addShare')">
              <template #icon><t-icon name="add" /></template>
            </t-button>
            <template #content>
              <div class="share-add-popup-inner" @click.stop>
                <div class="member-invite-popup-title">{{ $t('organization.share.addShareDialogTitle') }}</div>
                <div class="org-upgrade-fields">
                  <div class="org-upgrade-field">
                    <label class="org-upgrade-field-label">{{ $t('organization.share.selectOrg') }}</label>
                    <ShareToSpaceOrgSelect v-model="selectedOrgId" :organizations="availableOrganizations"
                      :loading="loadingOrgs" />
                  </div>
                  <div class="org-upgrade-field org-upgrade-field--last">
                    <label class="org-upgrade-field-label">{{ $t('organization.share.permission') }}</label>
                    <t-select v-model="selectedPermission" size="medium">
                      <t-option value="viewer" :label="$t('organization.share.permissionReadonly')" />
                      <t-option value="editor" :label="$t('organization.share.permissionEditable')" />
                    </t-select>
                    <p class="member-form-hint">{{ $t('organization.share.permissionTip') }}</p>
                  </div>
                </div>
                <div class="invite-popup-footer">
                  <t-button variant="outline" :disabled="submitting" @click="addPopupVisible = false">
                    {{ $t('common.cancel') }}
                  </t-button>
                  <t-button theme="primary" :loading="submitting" :disabled="!selectedOrgId" @click="handleShare">
                    {{ $t('knowledgeEditor.share.addShare') }}
                  </t-button>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
      </div>

      <div v-if="loadingShares && shares.length === 0" class="share-panel-loading">
        <t-loading size="small" />
        <span>{{ $t('organization.share.loading') }}</span>
      </div>
      <div v-else-if="filteredShares.length === 0" class="share-panel-empty">
        <t-empty :description="searchQuery.trim()
          ? $t('organization.share.emptySearch', { q: searchQuery })
          : $t('organization.share.noShares')" />
      </div>
      <div v-else class="share-panel-table-shell">
        <t-table row-key="id" :data="filteredShares" :columns="shareColumns" size="medium" hover stripe
          :loading="loadingShares">
          <template #space="{ row }">
            <div class="share-space-cell">
              <span class="share-space-name">
                <SpaceAvatar :name="row.organization_name || ''"
                  :avatar="getOrgForShare(row.organization_id)?.avatar" size="small" />
                <span class="share-space-name-text">{{ row.organization_name }}</span>
              </span>
              <span v-if="row.shared_by_username" class="share-space-meta">
                {{ $t('organization.share.sharedFrom') }} {{ row.shared_by_username }}
              </span>
            </div>
          </template>
          <template #permission="{ row }">
            <div class="share-permission-cell">
              <t-select v-if="canShare" :value="row.permission" size="small" class="share-permission-select"
                @change="(val: string) => handleUpdatePermission(row, val)">
                <t-option value="viewer" :label="$t('organization.share.permissionReadonly')" />
                <t-option value="editor" :label="$t('organization.share.permissionEditable')" />
              </t-select>
              <t-tag v-else size="small"
                :theme="row.permission === 'editor' || row.permission === 'admin' ? 'warning' : 'default'"
                variant="light">
                {{ permissionLabel(row.permission) }}
              </t-tag>
            </div>
          </template>
          <template #created_at="{ row }">{{ formatShareDate(row.created_at) }}</template>
          <template #actions="{ row }">
            <div v-if="canShare" class="share-table-actions">
              <t-popconfirm :content="$t('knowledgeEditor.share.unshareConfirm', { name: row.organization_name })"
                :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                :cancel-btn="{ content: $t('common.cancel') }" placement="left" @confirm="handleUnshare(row)">
                <t-tooltip :content="$t('organization.share.unshareAction')" placement="top">
                  <t-button theme="danger" shape="square" variant="text" size="small" @click.stop>
                    <template #icon><t-icon name="delete" /></template>
                  </t-button>
                </t-tooltip>
              </t-popconfirm>
            </div>
          </template>
        </t-table>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useOrganizationStore } from '@/stores/organization'
import { listKBShares } from '@/api/organization'
import type { KnowledgeBaseShare } from '@/api/organization'
import SpaceAvatar from '@/components/SpaceAvatar.vue'
import ShareToSpaceOrgSelect from '@/components/ShareToSpaceOrgSelect.vue'

const { t } = useI18n()
const orgStore = useOrganizationStore()

function getOrgForShare(organizationId: string) {
  return orgStore.organizations.find(o => o.id === organizationId)
}

interface Props {
  kbId: string
  canShare?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  canShare: false,
})

const shareHintPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(400px, calc(100vw - 24px))',
  maxWidth: 'min(400px, calc(100vw - 24px))',
  maxHeight: 'min(280px, 65vh)',
  overflow: 'hidden',
}

const loadingOrgs = ref(false)
const loadingShares = ref(false)
const submitting = ref(false)
const addPopupVisible = ref(false)
const searchQuery = ref('')
const selectedOrgId = ref('')
const selectedPermission = ref<'viewer' | 'editor'>('viewer')
const shares = ref<(KnowledgeBaseShare & { organization_name?: string })[]>([])

const availableOrganizations = computed(() => {
  const sharedOrgIds = new Set(shares.value.map(s => s.organization_id))
  return orgStore.organizations.filter(
    (org) =>
      !sharedOrgIds.has(org.id) &&
      (org.is_owner === true || org.my_role === 'admin' || org.my_role === 'editor')
  )
})

const filteredShares = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return shares.value
  return shares.value.filter((share) => {
    const haystack = [
      share.organization_name,
      share.shared_by_username,
      permissionLabel(share.permission),
    ].filter(Boolean).join(' ').toLowerCase()
    return haystack.includes(query)
  })
})

const shareColumns = computed(() => {
  const cols = [
    { colKey: 'space', title: t('organization.share.columns.space'), ellipsis: true, minWidth: 180 },
    { colKey: 'permission', title: t('organization.share.columns.permission'), width: 132 },
    { colKey: 'created_at', title: t('organization.share.columns.sharedAt'), width: 154 },
  ]
  if (props.canShare) {
    cols.push({ colKey: 'actions', title: t('organization.share.columns.operations'), width: 72, align: 'left' } as typeof cols[number])
  }
  return cols
})

function permissionLabel(permission: string) {
  if (permission === 'editor' || permission === 'admin') {
    return t('organization.share.permissionEditable')
  }
  return t('organization.share.permissionReadonly')
}

function formatShareDate(dateStr?: string) {
  if (!dateStr) return '—'
  const date = new Date(dateStr)
  if (Number.isNaN(date.getTime())) return dateStr
  return date.toLocaleDateString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit' })
}

async function loadOrganizations() {
  loadingOrgs.value = true
  try {
    await orgStore.fetchOrganizations()
  } finally {
    loadingOrgs.value = false
  }
}

async function loadShares() {
  if (!props.kbId) return
  loadingShares.value = true
  try {
    const result = await listKBShares(props.kbId)
    if (result.success && result.data) {
      const sharesData = (result.data as { shares?: KnowledgeBaseShare[] }).shares || result.data
      const sharesList = Array.isArray(sharesData) ? sharesData : []
      shares.value = sharesList.map((share: KnowledgeBaseShare) => ({
        ...share,
        organization_name: share.organization_name || orgStore.organizations.find(o => o.id === share.organization_id)?.name || share.organization_id
      }))
    }
  } catch (e) {
    console.error('Failed to load shares:', e)
  } finally {
    loadingShares.value = false
  }
}

async function handleShare() {
  if (!selectedOrgId.value) return

  submitting.value = true
  try {
    const result = await orgStore.shareKnowledgeBase(props.kbId, {
      organization_id: selectedOrgId.value,
      permission: selectedPermission.value
    })
    if (result.success) {
      MessagePlugin.success(t('organization.share.shareSuccess'))
      selectedOrgId.value = ''
      selectedPermission.value = 'viewer'
      addPopupVisible.value = false
      await loadShares()
    } else {
      MessagePlugin.error(result.message || t('organization.share.shareFailed'))
    }
  } catch (e: unknown) {
    const message = e instanceof Error ? e.message : t('organization.share.shareFailed')
    MessagePlugin.error(message)
  } finally {
    submitting.value = false
  }
}

async function handleUpdatePermission(share: KnowledgeBaseShare, newPermission: string) {
  if (share.permission === newPermission) return

  try {
    const result = await orgStore.changeKnowledgeBaseSharePermission(props.kbId, share.id, {
      permission: newPermission as 'viewer' | 'editor'
    })
    if (result.success) {
      MessagePlugin.success(t('organization.roleUpdated'))
      await loadShares()
    } else {
      MessagePlugin.error(result.message || t('organization.roleUpdateFailed'))
    }
  } catch (e: unknown) {
    const message = e instanceof Error ? e.message : t('organization.roleUpdateFailed')
    MessagePlugin.error(message)
  }
}

async function handleUnshare(share: KnowledgeBaseShare) {
  try {
    const result = await orgStore.unshareKnowledgeBase(
      props.kbId,
      share.id,
      share.organization_id
    )
    if (result.success) {
      MessagePlugin.success(t('organization.share.unshareSuccess'))
      await loadShares()
    } else {
      MessagePlugin.error(result.message || t('organization.share.unshareFailed'))
    }
  } catch (e: unknown) {
    const message = e instanceof Error ? e.message : t('organization.share.unshareFailed')
    MessagePlugin.error(message)
  }
}

watch(() => props.kbId, async (newKbId) => {
  if (newKbId) {
    await Promise.all([loadOrganizations(), loadShares()])
  }
}, { immediate: true })
</script>

<style scoped lang="less">
@import '@/components/share-to-space-panel.less';
</style>

<style lang="less">
@import '@/components/share-to-space-panel.overlay.less';
</style>
