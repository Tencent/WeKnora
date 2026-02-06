<template>
  <t-dialog
    v-model:visible="dialogVisible"
    :header="$t('organization.share.title')"
    width="520px"
    :footer="false"
    @close="handleClose"
  >
    <!-- Share form -->
    <div class="share-form" v-if="!showShareList">
      <t-form :data="shareForm" ref="shareFormRef">
        <t-form-item 
          :label="$t('organization.share.selectOrg')" 
          name="organization_id"
          :rules="[{ required: true, message: $t('organization.share.selectOrgPlaceholder') }]"
        >
          <t-select
            v-model="shareForm.organization_id"
            :placeholder="$t('organization.share.selectOrgPlaceholder')"
            :loading="loadingOrgs"
            class="org-select-dropdown"
            :popup-props="{ overlayClassName: 'org-select-dropdown-popup' }"
          >
            <t-option
              v-for="org in availableOrganizations"
              :key="org.id"
              :value="org.id"
              :label="org.name"
            >
              <div class="org-option-content">
                <div class="org-option-icon-wrap">
                  <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
                </div>
                <div class="org-option-body">
                  <div class="org-option-header">
                    <span class="org-option-name">{{ org.name }}</span>
                    <t-tag v-if="org.is_owner" theme="primary" size="small" variant="light">
                      {{ $t('organization.owner') }}
                    </t-tag>
                    <t-tag v-else-if="org.my_role" :theme="org.my_role === 'admin' ? 'warning' : 'default'" size="small" variant="light">
                      {{ $t(`organization.role.${org.my_role}`) }}
                    </t-tag>
                  </div>
                  <div class="org-option-meta">
                    <span class="org-meta-tag">
                      <t-icon name="user" class="org-meta-icon org-meta-icon-user" />
                      {{ org.member_count ?? 0 }}
                    </span>
                    <span class="org-meta-tag">
                      <img src="@/assets/img/zhishiku.svg" class="org-meta-icon org-meta-icon-kb" alt="" aria-hidden="true" />
                      {{ org.share_count ?? 0 }}
                    </span>
                    <span class="org-meta-tag">
                      <t-icon name="control-platform" class="org-meta-icon org-meta-icon-agent" />
                      {{ org.agent_share_count ?? 0 }}
                    </span>
                  </div>
                </div>
              </div>
            </t-option>
          </t-select>
        </t-form-item>
        <t-form-item :label="$t('organization.share.permission')" name="permission">
          <t-radio-group v-model="shareForm.permission">
            <t-radio value="viewer">{{ $t('organization.share.permissionReadonly') }}</t-radio>
            <t-radio value="editor">{{ $t('organization.share.permissionEditable') }}</t-radio>
          </t-radio-group>
        </t-form-item>
        <div class="permission-tip">
          <t-icon name="info-circle" size="14px" />
          <span>{{ $t('organization.share.permissionTip') }}</span>
        </div>
      </t-form>
      <div class="share-actions">
        <t-button theme="default" @click="showShareList = true" v-if="shares.length > 0">
          {{ $t('organization.share.sharedTo') }} ({{ shares.length }})
        </t-button>
        <div class="spacer"></div>
        <t-button theme="default" @click="handleClose">{{ $t('common.cancel') }}</t-button>
        <t-button theme="primary" :loading="submitting" @click="handleShare">
          {{ $t('common.confirm') }}
        </t-button>
      </div>
    </div>

    <!-- Share list -->
    <div class="share-list" v-else>
      <div class="share-list-header">
        <t-button variant="text" @click="showShareList = false">
          <template #icon><t-icon name="chevron-left" /></template>
          {{ $t('common.back') }}
        </t-button>
      </div>
      <div v-if="loadingShares" class="share-list-loading">
        <t-loading />
      </div>
      <div v-else-if="shares.length === 0" class="share-list-empty">
        {{ $t('organization.share.noShares') }}
      </div>
      <div v-else class="share-items">
        <div v-for="share in shares" :key="share.id" class="share-item">
          <div class="share-info">
            <SpaceAvatar
              :name="share.organization_name || ''"
              :avatar="orgStore.organizations.find(o => o.id === share.organization_id)?.avatar"
              size="small"
            />
            <span class="share-org-name">{{ share.organization_name }}</span>
            <t-tag :theme="share.permission === 'editor' ? 'warning' : 'default'" size="small">
              {{ share.permission === 'editor' ? $t('organization.share.permissionEditable') : $t('organization.share.permissionReadonly') }}
            </t-tag>
          </div>
          <div class="share-actions">
            <t-tooltip :content="$t('organization.settings.editTitle')" placement="top">
              <t-button variant="text" theme="default" size="small" @click="handleGoToOrgSettings(share.organization_id)">
                <t-icon name="setting" />
              </t-button>
            </t-tooltip>
            <t-button 
              variant="text" 
              theme="danger" 
              size="small"
              @click="handleUnshare(share)"
            >
              <t-icon name="close" />
            </t-button>
          </div>
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useOrganizationStore } from '@/stores/organization'
import { shareKnowledgeBase, listKBShares, removeShare } from '@/api/organization'
import type { KnowledgeBaseShare } from '@/api/organization'
import SpaceAvatar from '@/components/SpaceAvatar.vue'

const { t } = useI18n()
const router = useRouter()
const orgStore = useOrganizationStore()

interface Props {
  visible: boolean
  knowledgeBaseId: string
  knowledgeBaseName?: string
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'shared'): void
}>()

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const shareFormRef = ref()
const loadingOrgs = ref(false)
const loadingShares = ref(false)
const submitting = ref(false)
const showShareList = ref(false)
const shares = ref<(KnowledgeBaseShare & { organization_name?: string })[]>([])

const shareForm = ref({
  organization_id: '',
  permission: 'viewer' as 'admin' | 'editor' | 'viewer'
})

// Only show organizations where user can share (editor or admin); exclude viewer-only orgs and already shared
const availableOrganizations = computed(() => {
  const sharedOrgIds = new Set(shares.value.map(s => s.organization_id))
  return orgStore.organizations.filter(
    (org) =>
      !sharedOrgIds.has(org.id) &&
      (org.is_owner === true || org.my_role === 'admin' || org.my_role === 'editor')
  )
})

watch(() => props.visible, async (newVal) => {
  if (newVal) {
    showShareList.value = false
    shareForm.value = { organization_id: '', permission: 'viewer' }
    await Promise.all([
      loadOrganizations(),
      loadShares()
    ])
  }
})

async function loadOrganizations() {
  loadingOrgs.value = true
  try {
    await orgStore.fetchOrganizations()
  } finally {
    loadingOrgs.value = false
  }
}

async function loadShares() {
  if (!props.knowledgeBaseId) return
  loadingShares.value = true
  try {
    const result = await listKBShares(props.knowledgeBaseId)
    if (result.success && result.data) {
      // Enrich shares with organization names
      shares.value = result.data.shares.map((share: KnowledgeBaseShare) => ({
        ...share,
        organization_name: orgStore.organizations.find(o => o.id === share.organization_id)?.name || share.organization_id
      }))
    }
  } catch (e) {
    console.error('Failed to load shares:', e)
  } finally {
    loadingShares.value = false
  }
}

async function handleShare() {
  const valid = await shareFormRef.value?.validate()
  if (valid !== true) return

  submitting.value = true
  try {
    const result = await shareKnowledgeBase(
      props.knowledgeBaseId,
      { organization_id: shareForm.value.organization_id, permission: shareForm.value.permission }
    )
    if (result.success) {
      MessagePlugin.success(t('organization.share.shareSuccess'))
      await loadShares()
      shareForm.value = { organization_id: '', permission: 'viewer' }
      emit('shared')
    } else {
      MessagePlugin.error(result.message || t('organization.share.shareFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.share.shareFailed'))
  } finally {
    submitting.value = false
  }
}

async function handleUnshare(share: KnowledgeBaseShare) {
  try {
    const result = await removeShare(props.knowledgeBaseId, share.id)
    if (result.success) {
      MessagePlugin.success(t('organization.share.unshareSuccess'))
      await loadShares()
      emit('shared')
    } else {
      MessagePlugin.error(result.message || t('organization.share.unshareFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.share.unshareFailed'))
  }
}

function handleClose() {
  emit('update:visible', false)
}

// Navigate to organization settings
function handleGoToOrgSettings(orgId: string) {
  router.push({
    path: '/platform/organizations',
    query: { orgId }
  })
  // 关闭当前弹窗
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
.share-form {
  padding: 8px 0;
}

.permission-tip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px;
  background: var(--td-bg-color-container-hover);
  border-radius: 6px;
  margin-top: 8px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.5;
  
  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}

.share-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
  
  .spacer {
    flex: 1;
  }
}

.share-list-header {
  margin-bottom: 16px;
}

.share-list-loading,
.share-list-empty {
  display: flex;
  justify-content: center;
  padding: 32px;
  color: var(--td-text-color-secondary);
}

.share-items {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 280px;
  overflow-y: auto;
}

.share-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 16px;
  background: var(--td-bg-color-container-hover);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  transition: background 0.2s, border-color 0.2s;
}

.share-item:hover {
  background: var(--td-bg-color-container-active);
  border-color: var(--td-component-stroke);
}

.share-info {
  display: flex;
  align-items: center;
  gap: 10px;
}

.share-org-name {
  font-weight: 500;
}

.share-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

// Custom option styles for organization select (compact)
:deep(.t-select-option) {
  height: auto;
  align-items: center;
  padding: 6px 12px;
  border-radius: 4px;
  margin: 1px 6px;
  transition: background 0.15s ease;
}

:deep(.t-select-option:hover),
:deep(.t-select-option.t-is-selected) {
  background: var(--td-bg-color-container-hover);
}

:deep(.t-select-option__content) {
  width: 100%;
}

.org-option-content {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0;
  min-width: 260px;
  width: 100%;
}

.org-option-icon-wrap {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.org-option-body {
  flex: 1;
  min-width: 0;
}

.org-option-header {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 2px;

  .org-option-name {
    font-family: "PingFang SC";
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.org-option-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-family: "PingFang SC";
  font-size: 12px;
  color: var(--td-text-color-placeholder);

  .org-meta-tag {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 0px 4px;
    background: #f0f0f0;
    border-radius: 4px;
  }

  .org-meta-icon {
    flex-shrink: 0;
    vertical-align: middle;
    color: var(--td-text-color-secondary);
  }

  .org-meta-icon-user {
    font-size: 12px;
  }

  .org-meta-icon-kb {
    width: 12px;
    height: 12px;
    opacity: 0.75;
  }

  .org-meta-icon-agent {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    opacity: 0.75;
  }
}
</style>

<style lang="less">
// Global styles for organization select dropdown (compact)
.org-select-dropdown-popup.t-select__dropdown {
  padding: 4px 0;
  max-height: 320px;
  overflow-y: auto;
  border-radius: 6px;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
}

.org-select-dropdown-popup .t-select-option {
  height: auto;
  align-items: center;
  padding: 6px 12px;
  border-radius: 4px;
  margin: 1px 6px;
}

.org-select-dropdown-popup .t-select-option__content {
  width: 100%;
}
</style>
