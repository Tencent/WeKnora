<template>
  <div class="section-content">
    <div class="section-header">
      <h3 class="section-title">{{ $t('organization.share.title') }}</h3>
      <p class="section-desc">{{ $t('knowledgeEditor.share.description') }}</p>
    </div>
    <div class="section-body">
      <!-- 共享表单 -->
      <div class="share-form">
        <div class="form-item">
          <label class="form-label">{{ $t('organization.share.selectOrg') }}</label>
          <div class="share-input-row">
            <t-select
              v-model="selectedOrgId"
              :placeholder="$t('organization.share.selectOrgPlaceholder')"
              :loading="loadingOrgs"
              class="org-select"
            >
              <t-option
                v-for="org in availableOrganizations"
                :key="org.id"
                :value="org.id"
                :label="org.name"
              >
                <div class="org-option-content">
                  <div class="org-option-header">
                    <span class="org-option-name">{{ org.name }}</span>
                    <t-tag v-if="org.is_owner" theme="primary" size="small" variant="light">
                      {{ $t('organization.owner') }}
                    </t-tag>
                    <t-tag v-else-if="org.my_role" :theme="org.my_role === 'admin' ? 'warning' : 'default'" size="small" variant="light">
                      {{ $t(`organization.role.${org.my_role}`) }}
                    </t-tag>
                  </div>
                  <div v-if="org.description" class="org-option-desc">{{ org.description }}</div>
                  <div class="org-option-meta">
                    <span class="org-meta-item">
                      <t-icon name="usergroup" size="14px" />
                      {{ org.member_count || 0 }} {{ $t('organization.members') }}
                    </span>
                    <span v-if="org.share_count !== undefined" class="org-meta-item">
                      <t-icon name="share" size="14px" />
                      {{ org.share_count }} {{ $t('organization.share.sharedKBs') }}
                    </span>
                  </div>
                </div>
              </t-option>
            </t-select>
            <t-select
              v-model="selectedPermission"
              class="permission-select"
            >
              <t-option value="viewer" :label="$t('organization.share.permissionReadonly')" />
              <t-option value="editor" :label="$t('organization.share.permissionEditable')" />
            </t-select>
            <t-button
              theme="primary"
              :loading="submitting"
              :disabled="!selectedOrgId"
              @click="handleShare"
            >
              {{ $t('knowledgeEditor.share.addShare') }}
            </t-button>
          </div>
          <p class="form-tip">{{ $t('organization.share.permissionTip') }}</p>
        </div>
      </div>

      <!-- 已共享列表 -->
      <div class="shares-section">
        <div class="shares-header">
          <span class="shares-title">{{ $t('organization.share.sharedTo') }}</span>
          <span class="shares-count">{{ shares.length }}</span>
        </div>

        <div v-if="loadingShares" class="shares-loading">
          <t-loading size="small" />
          <span>{{ $t('common.loading') }}</span>
        </div>

        <div v-else-if="shares.length === 0" class="shares-empty">
          <t-icon name="share" class="empty-icon" />
          <span>{{ $t('organization.share.noShares') }}</span>
        </div>

        <div v-else class="shares-list">
          <div v-for="share in shares" :key="share.id" class="share-item">
            <div class="share-info">
              <div class="share-org">
                <t-icon name="usergroup" class="org-icon" />
                <span class="org-name">{{ share.organization_name }}</span>
              </div>
              <t-tag
                :theme="share.permission === 'editor' ? 'warning' : 'default'"
                size="small"
                variant="light"
              >
                {{ share.permission === 'editor' ? $t('organization.share.permissionEditable') : $t('organization.share.permissionReadonly') }}
              </t-tag>
            </div>
            <div class="share-actions">
              <t-select
                :value="share.permission"
                size="small"
                class="permission-change-select"
                @change="(val: string) => handleUpdatePermission(share, val)"
              >
                <t-option value="viewer" :label="$t('organization.share.permissionReadonly')" />
                <t-option value="editor" :label="$t('organization.share.permissionEditable')" />
              </t-select>
              <t-popconfirm
                :content="$t('knowledgeEditor.share.unshareConfirm', { name: share.organization_name })"
                @confirm="handleUnshare(share)"
              >
                <t-button variant="text" theme="danger" size="small">
                  <t-icon name="delete" />
                </t-button>
              </t-popconfirm>
            </div>
          </div>
        </div>
      </div>

      <!-- 提示信息 -->
      <div class="share-tips">
        <t-icon name="info-circle" class="tip-icon" />
        <div class="tip-content">
          <p>{{ $t('knowledgeEditor.share.tip1') }}</p>
          <p>{{ $t('knowledgeEditor.share.tip2') }}</p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useOrganizationStore } from '@/stores/organization'
import { shareKnowledgeBase, listKBShares, removeShare, updateSharePermission } from '@/api/organization'
import type { KnowledgeBaseShare } from '@/api/organization'

const { t } = useI18n()
const orgStore = useOrganizationStore()

interface Props {
  kbId: string
}

const props = defineProps<Props>()

const loadingOrgs = ref(false)
const loadingShares = ref(false)
const submitting = ref(false)
const selectedOrgId = ref('')
const selectedPermission = ref<'viewer' | 'editor'>('viewer')
const shares = ref<(KnowledgeBaseShare & { organization_name?: string })[]>([])

// Only show organizations where user can share (editor or admin); exclude viewer-only orgs and already shared
const availableOrganizations = computed(() => {
  const sharedOrgIds = new Set(shares.value.map(s => s.organization_id))
  return orgStore.organizations.filter(
    (org) =>
      !sharedOrgIds.has(org.id) &&
      (org.is_owner === true || org.my_role === 'admin' || org.my_role === 'editor')
  )
})

// Load organizations
async function loadOrganizations() {
  loadingOrgs.value = true
  try {
    await orgStore.fetchOrganizations()
  } finally {
    loadingOrgs.value = false
  }
}

// Load shares
async function loadShares() {
  if (!props.kbId) return
  loadingShares.value = true
  try {
    const result = await listKBShares(props.kbId)
    if (result.success && result.data) {
      // result.data is ListSharesResponse with shares array
      const sharesData = (result.data as any).shares || result.data
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

// Handle share
async function handleShare() {
  if (!selectedOrgId.value) return

  submitting.value = true
  try {
    const result = await shareKnowledgeBase(props.kbId, {
      organization_id: selectedOrgId.value,
      permission: selectedPermission.value
    })
    if (result.success) {
      MessagePlugin.success(t('organization.share.shareSuccess'))
      selectedOrgId.value = ''
      selectedPermission.value = 'viewer'
      await loadShares()
    } else {
      MessagePlugin.error(result.message || t('organization.share.shareFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.share.shareFailed'))
  } finally {
    submitting.value = false
  }
}

// Handle update permission
async function handleUpdatePermission(share: KnowledgeBaseShare, newPermission: string) {
  if (share.permission === newPermission) return

  try {
    const result = await updateSharePermission(props.kbId, share.id, {
      permission: newPermission as 'viewer' | 'editor'
    })
    if (result.success) {
      MessagePlugin.success(t('organization.roleUpdated'))
      await loadShares()
    } else {
      MessagePlugin.error(result.message || t('organization.roleUpdateFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.roleUpdateFailed'))
  }
}

// Handle unshare
async function handleUnshare(share: KnowledgeBaseShare) {
  try {
    const result = await removeShare(props.kbId, share.id)
    if (result.success) {
      MessagePlugin.success(t('organization.share.unshareSuccess'))
      await loadShares()
    } else {
      MessagePlugin.error(result.message || t('organization.share.unshareFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.share.unshareFailed'))
  }
}

// Watch for kbId changes
watch(() => props.kbId, async (newKbId) => {
  if (newKbId) {
    await Promise.all([loadOrganizations(), loadShares()])
  }
}, { immediate: true })

onMounted(async () => {
  if (props.kbId) {
    await Promise.all([loadOrganizations(), loadShares()])
  }
})
</script>

<style scoped lang="less">
.section-content {
  .section-header {
    margin-bottom: 20px;
  }

  .section-title {
    margin: 0 0 8px 0;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    color: #000000e6;
  }

  .section-desc {
    margin: 0;
    font-family: "PingFang SC";
    font-size: 14px;
    color: #00000066;
    line-height: 22px;
  }
}

.share-form {
  margin-bottom: 24px;
  padding-bottom: 24px;
  border-bottom: 1px solid #f0f0f0;
}

.form-item {
  .form-label {
    display: block;
    margin-bottom: 8px;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }

  .form-tip {
    margin-top: 8px;
    font-size: 12px;
    color: #00000066;
    line-height: 18px;
  }
}

.share-input-row {
  display: flex;
  gap: 12px;
  align-items: center;

  .org-select {
    flex: 1;
    min-width: 200px;
  }

  .permission-select {
    width: 120px;
  }
}

.shares-section {
  margin-bottom: 24px;
}

.shares-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;

  .shares-title {
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }

  .shares-count {
    padding: 2px 8px;
    background: #f5f5f5;
    border-radius: 10px;
    font-size: 12px;
    color: #00000066;
  }
}

.shares-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 32px;
  color: #00000066;
  font-size: 14px;
}

.shares-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 40px 20px;
  background: #fafafa;
  border-radius: 8px;
  color: #00000066;

  .empty-icon {
    font-size: 32px;
    opacity: 0.5;
  }
}

.shares-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.share-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: #fafafa;
  border-radius: 8px;
  transition: background 0.2s ease;

  &:hover {
    background: #f5f5f5;
  }
}

.share-info {
  display: flex;
  align-items: center;
  gap: 12px;
}

.share-org {
  display: flex;
  align-items: center;
  gap: 8px;

  .org-icon {
    font-size: 16px;
    color: #0052d9;
  }

  .org-name {
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }
}

.share-actions {
  display: flex;
  align-items: center;
  gap: 8px;

  .permission-change-select {
    width: 100px;
  }
}

.share-tips {
  display: flex;
  gap: 12px;
  padding: 16px;
  background: #f0f7ff;
  border-radius: 8px;
  border: 1px solid #e6f0ff;

  .tip-icon {
    flex-shrink: 0;
    font-size: 16px;
    color: #0052d9;
    margin-top: 2px;
  }

  .tip-content {
    flex: 1;

    p {
      margin: 0 0 4px 0;
      font-size: 13px;
      color: #00000099;
      line-height: 20px;

      &:last-child {
        margin-bottom: 0;
      }
    }
  }
}

// Custom option styles for organization select
:deep(.t-select-option) {
  height: auto;
  align-items: flex-start;
  padding-top: 8px;
  padding-bottom: 8px;
}

:deep(.t-select-option__content) {
  white-space: normal;
  width: 100%;
}

.org-option-content {
  padding: 2px 0;
  min-width: 280px;
  width: 100%;
}

.org-option-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;

  .org-option-name {
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }
}

.org-option-desc {
  font-family: "PingFang SC";
  font-size: 12px;
  color: #00000066;
  line-height: 18px;
  margin-bottom: 6px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.org-option-meta {
  display: flex;
  align-items: center;
  gap: 16px;
  font-family: "PingFang SC";
  font-size: 12px;
  color: #00000066;

  .org-meta-item {
    display: flex;
    align-items: center;
    gap: 4px;
  }
}
</style>

<style lang="less">
// Global styles for select dropdown (outside scoped)
.t-select__dropdown .t-select-option {
  height: auto;
  align-items: flex-start;
  padding-top: 8px;
  padding-bottom: 8px;
}

.t-select__dropdown .t-select-option__content {
  white-space: normal;
  width: 100%;
}
</style>
