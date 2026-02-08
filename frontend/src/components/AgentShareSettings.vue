<template>
  <div class="section-content">
    <div class="section-header">
      <h3 class="section-title">{{ $t('organization.share.title') }}</h3>
      <p class="section-desc">{{ $t('organization.share.agentShareDesc') || '将智能体共享到空间，空间成员可使用该智能体' }}</p>
    </div>
    <!-- 共享范围说明：当传入 agent 时展示，仅提示 + 变更同步说明，不列具体开关 -->
    <div v-if="agent?.config" class="share-scope-block">
      <h4 class="share-scope-title">{{ $t('agent.shareScope.title') }}</h4>
      <p class="share-scope-desc">{{ $t('agent.shareScope.desc') }}</p>
    </div>
    <div class="section-body">
      <div class="share-form">
        <div class="form-item">
          <label class="form-label">{{ $t('organization.share.selectOrg') }}</label>
          <div class="share-input-row">
            <t-select
              v-model="selectedOrgId"
              :placeholder="$t('organization.share.selectOrgPlaceholder')"
              :loading="loadingOrgs"
              class="org-select org-select-dropdown"
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
                        <img src="@/assets/img/agent.svg" class="org-meta-icon org-meta-icon-agent" alt="" aria-hidden="true" />
                        {{ org.agent_share_count ?? 0 }}
                      </span>
                    </div>
                  </div>
                </div>
              </t-option>
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
        </div>
      </div>
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
              <div class="share-info-top">
                <div class="share-org">
                  <SpaceAvatar
                    :name="share.organization_name || ''"
                    :avatar="orgStore.organizations.find(o => o.id === share.organization_id)?.avatar"
                    size="small"
                  />
                  <span class="org-name">{{ share.organization_name }}</span>
                </div>
              </div>
              <div class="share-item-meta">
                <span class="org-meta-tag">
                  <t-icon name="user" class="org-meta-icon org-meta-icon-user" />
                  {{ getOrgForShare(share.organization_id)?.member_count ?? 0 }}
                </span>
                <span class="org-meta-tag">
                  <img src="@/assets/img/zhishiku.svg" class="org-meta-icon org-meta-icon-kb" alt="" aria-hidden="true" />
                  {{ getOrgForShare(share.organization_id)?.share_count ?? 0 }}
                </span>
                <t-tooltip :content="$t('organization.share.spaceAgentShareCountTip')" placement="top">
                  <span class="org-meta-tag">
                    <img src="@/assets/img/agent.svg" class="org-meta-icon org-meta-icon-agent" alt="" aria-hidden="true" />
                    {{ getOrgForShare(share.organization_id)?.agent_share_count ?? 0 }}
                  </span>
                </t-tooltip>
              </div>
            </div>
            <div class="share-actions">
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
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useOrganizationStore } from '@/stores/organization'
import { shareAgent, listAgentShares, removeAgentShare } from '@/api/organization'
import type { AgentShareResponse } from '@/api/organization'
import type { CustomAgent } from '@/api/agent'
import SpaceAvatar from '@/components/SpaceAvatar.vue'

const { t } = useI18n()
const orgStore = useOrganizationStore()

function getOrgForShare(organizationId: string) {
  return orgStore.organizations.find(o => o.id === organizationId)
}

interface Props {
  agentId: string
  /** 当前智能体（用于展示共享范围说明） */
  agent?: CustomAgent | null
}

const props = defineProps<Props>()

const loadingOrgs = ref(false)
const loadingShares = ref(false)
const submitting = ref(false)
const selectedOrgId = ref('')
const shares = ref<(AgentShareResponse & { organization_name?: string })[]>([])

const availableOrganizations = computed(() => {
  const sharedOrgIds = new Set(shares.value.map(s => s.organization_id))
  return orgStore.organizations.filter(
    (org) =>
      !sharedOrgIds.has(org.id) &&
      (org.is_owner === true || org.my_role === 'admin' || org.my_role === 'editor')
  )
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
  if (!props.agentId) return
  loadingShares.value = true
  try {
    const result = await listAgentShares(props.agentId)
    if (result.success && result.data) {
      const sharesData = (result.data as any).shares || result.data
      const sharesList = Array.isArray(sharesData) ? sharesData : []
      shares.value = sharesList.map((share: AgentShareResponse) => ({
        ...share,
        organization_name: share.organization_name || orgStore.organizations.find(o => o.id === share.organization_id)?.name || share.organization_id
      }))
    }
  } catch (e) {
    console.error('Failed to load agent shares:', e)
  } finally {
    loadingShares.value = false
  }
}

async function handleShare() {
  if (!selectedOrgId.value) return
  submitting.value = true
  try {
    const result = await shareAgent(props.agentId, {
      organization_id: selectedOrgId.value,
      permission: 'viewer'
    })
    if (result.success) {
      MessagePlugin.success(t('organization.share.shareSuccess'))
      selectedOrgId.value = ''
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

async function handleUnshare(share: AgentShareResponse) {
  try {
    const result = await removeAgentShare(props.agentId, share.id)
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

watch(() => props.agentId, async (newId) => {
  if (newId) await Promise.all([loadOrganizations(), loadShares()])
}, { immediate: true })

onMounted(async () => {
  if (props.agentId) await Promise.all([loadOrganizations(), loadShares()])
})

defineExpose({ loadShares })
</script>

<style scoped lang="less">
.section-content { .section-header { margin-bottom: 20px; } .section-title { margin: 0 0 8px 0; font-size: 16px; font-weight: 600; } .section-desc { margin: 0; font-size: 14px; color: #00000066; } }
.share-form { margin-bottom: 24px; padding-bottom: 24px; border-bottom: 1px solid #f0f0f0; }
.form-item {
  .form-label {
    display: block;
    margin-bottom: 12px;
    font-size: 14px;
    font-weight: 500;
  }
}
.share-input-row {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
  .org-select { flex: 1; min-width: 240px; }
}
.shares-section { margin-bottom: 24px; }
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
  .empty-icon { font-size: 32px; opacity: 0.5; }
}
.shares-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 320px;
  overflow-y: auto;
}
.share-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  background: #fafafa;
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  transition: background 0.2s ease, border-color 0.2s ease;
  &:hover {
    background: #f5f5f5;
    border-color: #e8e8e8;
  }
}
.share-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.share-info-top {
  display: flex;
  align-items: center;
  gap: 12px;
}
.share-org {
  display: flex;
  align-items: center;
  gap: 8px;
  .org-name {
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }
}
.share-item-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: #00000099;
  .org-meta-tag {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 2px 6px;
    background: #f0f0f0;
    border-radius: 4px;
  }
  .org-meta-icon {
    flex-shrink: 0;
    vertical-align: middle;
    color: #00000099;
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
    width: 12px;
    height: 12px;
    opacity: 0.75;
  }
}
.share-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  .permission-change-select { width: 100px; }
}

.share-scope-block {
  margin-bottom: 24px;
  padding: 16px;
  background: #f8faf9;
  border: 1px solid #e8f5ed;
  border-radius: 8px;
}
.share-scope-title {
  margin: 0 0 6px 0;
  font-size: 14px;
  font-weight: 600;
  color: #1a1a1a;
}
.share-scope-desc {
  margin: 0 0 12px 0;
  font-size: 12px;
  color: #666;
  line-height: 1.4;
}

// 与知识库设置中空间下拉一致的选项样式
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
  background: #f0f7ff;
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
}
.org-option-name {
  font-family: "PingFang SC";
  font-size: 13px;
  font-weight: 500;
  color: #000000e6;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.org-option-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-family: "PingFang SC";
  font-size: 12px;
  color: #00000099;

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
    color: #00000099;
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
    width: 12px;
    height: 12px;
    opacity: 0.75;
  }
}
</style>

<style lang="less">
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
