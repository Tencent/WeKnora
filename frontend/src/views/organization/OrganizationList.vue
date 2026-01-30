<template>
  <div class="org-list-container">
    <!-- 头部 -->
    <div class="header">
      <div class="header-title">
        <h2>{{ $t('organization.title') }}</h2>
        <p class="header-subtitle">{{ $t('organization.subtitle') }}</p>
      </div>
      <div class="header-actions">
        <t-button theme="default" variant="outline" class="org-join-btn" @click="handleJoinOrganization">
          <template #icon><t-icon name="enter" /></template>
          {{ $t('organization.joinOrg') }}
        </t-button>
        <t-button class="org-create-btn" @click="handleCreateOrganization">
          <template #icon><t-icon name="usergroup-add" /></template>
          {{ $t('organization.createOrg') }}
        </t-button>
      </div>
    </div>
    <!-- Tab 切换（下划线式，替代分隔线） -->
    <div class="org-tabs">
      <div
        class="tab-item"
        :class="{ 'active': activeTab === 'all' }"
        @click="activeTab = 'all'"
      >
        {{ $t('organization.all') }} ({{ organizations.length }})
      </div>
      <div
        class="tab-item"
        :class="{ 'active': activeTab === 'created' }"
        @click="activeTab = 'created'"
      >
        {{ $t('organization.createdByMe') }} ({{ organizations.filter(o => o.is_owner).length }})
      </div>
      <div
        class="tab-item"
        :class="{ 'active': activeTab === 'joined' }"
        @click="activeTab = 'joined'"
      >
        {{ $t('organization.joinedByMe') }} ({{ organizations.filter(o => !o.is_owner).length }})
      </div>
    </div>

    <!-- 卡片网格 -->
    <div v-if="filteredOrganizations.length > 0" class="org-card-wrap">
      <div
        v-for="(org, index) in filteredOrganizations"
        :key="org.id"
        class="org-card"
        :class="{ 'joined-org': !org.is_owner }"
        @click="handleCardClick(org)"
      >
        <!-- 装饰图标 - 无限/连接/圈子 -->
        <div class="card-decoration">
          <svg class="org-icon" width="28" height="28" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M3 10 A4 4 0 0 1 11 10 A4 4 0 0 1 19 10 A4 4 0 0 1 11 10 A4 4 0 0 1 3 10" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" fill="none" opacity="0.9"/>
          </svg>
          <svg class="connect-icon" width="16" height="16" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M3 10 A4 4 0 0 1 11 10 A4 4 0 0 1 19 10 A4 4 0 0 1 11 10 A4 4 0 0 1 3 10" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" fill="none" opacity="0.7"/>
          </svg>
        </div>

        <!-- 卡片头部 -->
        <div class="card-header">
          <div class="card-header-left">
            <!-- 空间头像：根据名称生成首字母+渐变（与智能体一致） -->
            <div class="org-avatar">
              <SpaceAvatar :name="org.name" />
            </div>
            <div class="card-title-block">
              <span class="card-title" :title="org.name">{{ org.name }}</span>
              <span v-if="!org.is_owner" class="card-subtitle">{{ $t('organization.joinedByMe') }}</span>
            </div>
          </div>
          <t-popup
            v-model="org.showMore"
            overlayClassName="card-more-popup"
            :on-visible-change="(visible: boolean) => onVisibleChange(visible, org)"
            trigger="click"
            destroy-on-close
            placement="bottom-right"
          >
            <div
              class="more-wrap"
              @click.stop
              :class="{ 'active-more': org.showMore }"
            >
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu" @click.stop>
                <div class="popup-menu-item" @click.stop="handleSettings(org)">
                  <t-icon class="menu-icon" name="setting" />
                  <span>{{ $t('organization.settings.editTitle') }}</span>
                </div>
                <div v-if="!org.is_owner" class="popup-menu-item delete" @click.stop="handleLeave(org)">
                  <t-icon class="menu-icon" name="logout" />
                  <span>{{ $t('organization.leave') }}</span>
                </div>
                <div v-if="org.is_owner" class="popup-menu-item delete" @click.stop="handleDelete(org)">
                  <t-icon class="menu-icon" name="delete" />
                  <span>{{ $t('common.delete') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>

        <!-- 卡片内容 -->
        <div class="card-content">
          <div class="card-description">
            {{ org.description || $t('organization.noDescription') }}
          </div>
        </div>

        <!-- 卡片底部 -->
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tooltip :content="$t('organization.memberCount')" placement="top">
                <div class="feature-badge member-badge">
                  <t-icon name="user" size="14px" />
                  <span class="badge-count">{{ org.member_count || 0 }}</span>
                </div>
              </t-tooltip>
              <t-tooltip :content="$t('organization.invite.knowledgeBases')" placement="top">
                <div class="feature-badge share-badge">
                  <t-icon name="folder" size="14px" />
                  <span class="badge-count">{{ org.share_count ?? 0 }}</span>
                </div>
              </t-tooltip>
              <t-tooltip v-if="(org.pending_join_request_count ?? 0) > 0" :content="$t('organization.settings.pendingJoinRequestsBadge')" placement="top">
                <span class="pending-requests-badge">{{ org.pending_join_request_count }} {{ $t('organization.settings.pendingReview') }}</span>
              </t-tooltip>
              <t-tag v-if="org.is_owner" class="role-tag owner" size="small">
                {{ $t('organization.owner') }}
              </t-tag>
              <t-tag v-else-if="org.my_role" class="role-tag" :class="org.my_role" size="small">
                {{ $t(`organization.role.${org.my_role}`) }}
              </t-tag>
            </div>
          </div>
          <span class="card-time">{{ formatDate(org.created_at) }}</span>
        </div>
      </div>
    </div>

    <!-- 空状态（按 Tab 显示不同文案） -->
    <div v-else-if="!loading" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ emptyStateTitle }}</span>
      <span class="empty-desc">{{ emptyStateDesc }}</span>
    </div>

    <!-- Organization Settings Modal (用于创建和编辑组织) -->
    <OrganizationSettingsModal
      :visible="showSettingsModal"
      :org-id="settingsOrgId"
      :mode="settingsMode"
      @update:visible="showSettingsModal = $event"
      @saved="handleSettingsSaved"
    />

    <!-- Delete Confirm Dialog -->
    <t-dialog
      v-model:visible="deleteVisible"
      dialogClassName="del-org-dialog"
      :closeBtn="false"
      :cancelBtn="null"
      :confirmBtn="null"
    >
      <div class="circle-wrap">
        <div class="dialog-header">
          <img class="circle-img" src="@/assets/img/circle.png" alt="">
          <span class="circle-title">{{ $t('organization.deleteConfirmTitle') }}</span>
        </div>
        <span class="del-circle-txt">
          {{ $t('organization.deleteConfirmMessage', { name: deletingOrg?.name ?? '' }) }}
        </span>
        <div class="circle-btn">
          <span class="circle-btn-txt" @click="deleteVisible = false">{{ $t('common.cancel') }}</span>
          <span class="circle-btn-txt confirm" @click="confirmDelete">{{ $t('common.delete') }}</span>
        </div>
      </div>
    </t-dialog>

    <!-- Leave Confirm Dialog -->
    <t-dialog
      v-model:visible="leaveVisible"
      dialogClassName="del-org-dialog"
      :closeBtn="false"
      :cancelBtn="null"
      :confirmBtn="null"
    >
      <div class="circle-wrap">
        <div class="dialog-header">
          <img class="circle-img" src="@/assets/img/circle.png" alt="">
          <span class="circle-title">{{ $t('organization.leaveConfirmTitle') }}</span>
        </div>
        <span class="del-circle-txt">
          {{ $t('organization.leaveConfirmMessage', { name: leavingOrg?.name ?? '' }) }}
        </span>
        <div class="circle-btn">
          <span class="circle-btn-txt" @click="leaveVisible = false">{{ $t('common.cancel') }}</span>
          <span class="circle-btn-txt confirm" @click="confirmLeave">{{ $t('organization.leave') }}</span>
        </div>
      </div>
    </t-dialog>

    <!-- 加入组织 / 邀请预览弹框（菜单与邀请链接共用同一弹框） -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="showInvitePreview" class="invite-preview-overlay" @click.self="closeInvitePreview">
          <div class="invite-preview-modal">
            <div class="invite-preview-header">
              <h2 class="invite-preview-title">{{ invitePreviewData ? $t('organization.invite.previewTitle') : $t('organization.joinOrg') }}</h2>
              <button class="invite-preview-close" @click="closeInvitePreview" :aria-label="$t('common.close')">
                <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                  <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
                </svg>
              </button>
            </div>

            <!-- 步骤1：输入邀请码 或 搜索空间 -->
            <div v-if="!invitePreviewLoading && !invitePreviewData" class="invite-preview-body invite-preview-input">
              <div class="join-modal-tabs">
                <div
                  :class="['join-tab', { active: joinStep === 'invite' }]"
                  @click="joinStep = 'invite'"
                >
                  {{ $t('organization.join.byInviteCode') }}
                </div>
                <div
                  :class="['join-tab', { active: joinStep === 'search' }]"
                  @click="joinStep = 'search'; doSearchSearchable()"
                >
                  {{ $t('organization.join.searchSpaces') }}
                </div>
              </div>

              <!-- Tab 内容容器 - 平滑高度过渡 -->
              <div ref="tabContentWrapperRef" class="join-tab-content-wrapper">
                <!-- 输入邀请码 -->
                <div v-if="joinStep === 'invite'" class="join-tab-content">
                  <template v-if="!invitePreviewError">
                    <p class="invite-preview-input-desc">{{ $t('organization.invite.inputDesc') }}</p>
                    <div class="invite-preview-input-wrap">
                      <t-input
                        v-model="joinInputCode"
                        :placeholder="$t('organization.inviteCodePlaceholder')"
                        size="medium"
                        :maxlength="32"
                        clearable
                        @keyup.enter="doPreviewFromInput"
                      />
                    </div>
                    <p class="invite-preview-input-tip">{{ $t('organization.editor.inviteCodeTip') }}</p>
                  </template>
                  <template v-else>
                    <div class="invite-preview-error-inline">
                      <t-icon name="error-circle" size="20px" />
                      <span>{{ invitePreviewError }}</span>
                    </div>
                    <div class="invite-preview-input-wrap">
                      <t-input
                        v-model="joinInputCode"
                        :placeholder="$t('organization.inviteCodePlaceholder')"
                        size="medium"
                        :maxlength="32"
                        clearable
                        @keyup.enter="doPreviewFromInput"
                      />
                    </div>
                  </template>
                  <div class="invite-preview-footer invite-preview-footer-single">
                    <t-button theme="default" variant="outline" size="medium" @click="closeInvitePreview">
                      {{ $t('common.cancel') }}
                    </t-button>
                    <t-button theme="primary" size="medium" :loading="invitePreviewLoading" @click="doPreviewFromInput">
                      {{ $t('organization.invite.previewAction') }}
                    </t-button>
                  </div>
                </div>

                <!-- 搜索可加入空间 -->
                <div v-else-if="joinStep === 'search'" class="join-tab-content">
                  <p class="invite-preview-input-desc">{{ $t('organization.join.searchSpacesDesc') }}</p>
                  <div class="invite-preview-input-wrap search-input-wrap">
                    <t-input
                      v-model="searchQuery"
                      :placeholder="$t('organization.join.searchSpacesPlaceholder')"
                      size="medium"
                      clearable
                      @input="doSearchSearchableDebounced"
                      @keyup.enter="doSearchSearchable"
                    >
                      <template #prefix-icon>
                        <t-icon name="search" />
                      </template>
                    </t-input>
                  </div>
                  <div class="searchable-list-wrap">
                    <t-loading :loading="searchLoading">
                      <div v-if="searchableList.length === 0 && !searchLoading" class="searchable-empty">
                        {{ searchQuery ? $t('organization.join.noSearchResult') : $t('organization.join.noSearchableSpaces') }}
                      </div>
                      <div v-else class="searchable-list">
                        <div
                          v-for="org in searchableList"
                          :key="org.id"
                          class="searchable-item"
                          :class="{ 'is-already-member': org.is_already_member }"
                          @click="!org.is_already_member && previewSearchableOrg(org)"
                        >
                          <div class="searchable-item-left">
                            <div class="searchable-item-avatar">
                              <SpaceAvatar :name="org.name" size="small" />
                            </div>
                            <div class="searchable-item-info">
                              <span class="searchable-item-name">{{ org.name }}</span>
                              <span class="searchable-item-desc">{{ org.description || $t('organization.noDescription') }}</span>
                              <div class="searchable-item-badges">
                                <span class="badge">{{ org.member_count }} {{ $t('organization.invite.members') }}</span>
                                <span class="badge">{{ org.share_count }} {{ $t('organization.invite.knowledgeBases') }}</span>
                                <t-tag v-if="org.require_approval" size="small" theme="warning" class="approval-tag">
                                  {{ $t('organization.invite.needApproval') }}
                                </t-tag>
                              </div>
                            </div>
                          </div>
                          <div class="searchable-item-action" @click.stop>
                            <t-button
                              v-if="org.is_already_member"
                              theme="default"
                              variant="outline"
                              size="small"
                              @click="viewSearchableOrg(org)"
                            >
                              {{ $t('organization.invite.viewOrganization') }}
                            </t-button>
                            <t-button
                              v-else
                              theme="primary"
                              size="small"
                              @click="previewSearchableOrg(org)"
                            >
                              {{ $t('organization.invite.previewAction') }}
                            </t-button>
                          </div>
                        </div>
                      </div>
                    </t-loading>
                  </div>
                  <div class="invite-preview-footer invite-preview-footer-single">
                    <t-button theme="default" variant="outline" size="medium" @click="closeInvitePreview">
                      {{ $t('common.cancel') }}
                    </t-button>
                  </div>
                </div>
              </div>
            </div>

            <!-- Loading -->
            <div v-else-if="invitePreviewLoading" class="invite-preview-body invite-preview-loading">
              <t-loading size="large" />
              <span class="invite-preview-loading-text">{{ $t('organization.invite.loading') }}</span>
            </div>

            <!-- 步骤2：预览内容 -->
            <template v-else-if="invitePreviewData">
              <div class="invite-preview-body invite-preview-body-preview">
                <!-- 空间信息 -->
                <div class="invite-preview-org">
                  <div class="invite-preview-org-icon">
                    <SpaceAvatar :name="invitePreviewData.name" size="large" />
                  </div>
                  <div class="invite-preview-org-info">
                    <h2 class="invite-preview-org-name">{{ invitePreviewData.name }}</h2>
                    <p class="invite-preview-org-desc">{{ invitePreviewData.description || $t('organization.noDescription') }}</p>
                  </div>
                </div>

                <!-- 统计信息 -->
                <div class="invite-preview-stats">
                  <div class="invite-preview-stat">
                    <t-icon name="user" class="invite-preview-stat-icon" />
                    <span class="invite-preview-stat-value">{{ invitePreviewData.member_count }}</span>
                    <span class="invite-preview-stat-label">{{ $t('organization.invite.members') }}</span>
                  </div>
                  <div class="invite-preview-stat">
                    <t-icon name="folder" class="invite-preview-stat-icon" />
                    <span class="invite-preview-stat-value">{{ invitePreviewData.share_count }}</span>
                    <span class="invite-preview-stat-label">{{ $t('organization.invite.knowledgeBases') }}</span>
                  </div>
                </div>

                <!-- 加入方式与状态 -->
                <div v-if="!invitePreviewData.is_already_member" class="invite-preview-join-section">
                  <div class="invite-preview-approval-row">
                    <span class="invite-preview-approval-label">{{ $t('organization.invite.approvalLabel') }}</span>
                    <span :class="['invite-preview-approval-badge', invitePreviewData.require_approval ? 'need-approval' : 'no-approval']">
                      {{ invitePreviewData.require_approval ? $t('organization.invite.needApproval') : $t('organization.invite.noApproval') }}
                    </span>
                  </div>
                  <div v-if="!invitePreviewData.require_approval" class="invite-preview-tip invite-preview-tip-info">
                    <t-icon name="info-circle" size="16px" />
                    <span>{{ $t('organization.invite.defaultRoleAfterJoin', { role: $t('organization.role.viewer') }) }}</span>
                  </div>
                  <template v-else>
                    <div class="invite-preview-tip invite-preview-tip-warning">
                      <t-icon name="info-circle" size="16px" />
                      <span>{{ $t('organization.invite.requireApprovalTip') }}</span>
                    </div>
                    <div class="invite-preview-form-group">
                      <label class="invite-preview-form-label">{{ $t('organization.invite.requestRole') }}</label>
                      <t-select
                        v-model="inviteRequestRole"
                        class="invite-preview-role-select"
                        size="medium"
                        :placeholder="$t('organization.invite.selectRole')"
                        :options="orgRoleOptions"
                      />
                    </div>
                    <div class="invite-preview-form-group">
                      <label class="invite-preview-form-label">{{ $t('organization.invite.applicationNote') }}</label>
                      <t-textarea
                        v-model="inviteRequestMessage"
                        class="invite-preview-message-input"
                        size="medium"
                        :placeholder="$t('organization.invite.messagePlaceholder')"
                        :maxlength="500"
                        :autosize="{ minRows: 2, maxRows: 4 }"
                      />
                    </div>
                  </template>
                </div>

                <div v-if="invitePreviewData.is_already_member" class="invite-preview-status-section">
                  <div class="invite-preview-tip invite-preview-tip-success">
                    <t-icon name="check-circle" size="18px" />
                    <span>{{ $t('organization.invite.alreadyMember') }}</span>
                  </div>
                </div>
              </div>

              <div class="invite-preview-footer">
                <t-button theme="default" variant="outline" size="medium" @click="closeInvitePreview">
                  {{ $t('common.cancel') }}
                </t-button>
                <t-button
                  v-if="!invitePreviewData.is_already_member"
                  theme="primary"
                  size="medium"
                  :loading="inviteJoining"
                  @click="confirmJoinOrganization"
                >
                  {{ invitePreviewData.require_approval ? $t('organization.invite.submitRequest') : $t('organization.invite.primaryJoin') }}
                </t-button>
                <t-button
                  v-else
                  theme="primary"
                  size="medium"
                  @click="viewOrganizationFromPreview"
                >
                  {{ $t('organization.invite.viewOrganization') }}
                </t-button>
              </div>
            </template>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useOrganizationStore } from '@/stores/organization'
import type { Organization, OrganizationPreview, SearchableOrganizationItem } from '@/api/organization'
import { previewOrganization, joinOrganization, submitJoinRequest, searchSearchableOrganizations, joinOrganizationById } from '@/api/organization'
import { useI18n } from 'vue-i18n'
import OrganizationSettingsModal from './OrganizationSettingsModal.vue'
import SpaceAvatar from '@/components/SpaceAvatar.vue'

interface OrgWithUI extends Organization {
  showMore?: boolean
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const orgStore = useOrganizationStore()

// 申请加入时可选角色（仅需审核时使用）
const orgRoleOptions = [
  { label: t('organization.role.viewer'), value: 'viewer' },
  { label: t('organization.role.editor'), value: 'editor' },
  { label: t('organization.role.admin'), value: 'admin' },
]
const inviteRequestRole = ref<'viewer' | 'editor' | 'admin'>('viewer')
const inviteRequestMessage = ref('')

// State
const showSettingsModal = ref(false)
const settingsOrgId = ref('')
const settingsMode = ref<'create' | 'edit'>('edit')
const deleteVisible = ref(false)
const leaveVisible = ref(false)
const deletingOrg = ref<Organization | null>(null)
const leavingOrg = ref<Organization | null>(null)

// 邀请预览相关状态（与邀请链接共用同一弹框）
const showInvitePreview = ref(false)
const invitePreviewLoading = ref(false)
const inviteJoining = ref(false)
const inviteCode = ref('')
const joinInputCode = ref('') // 从菜单打开时输入的邀请码
const invitePreviewData = ref<OrganizationPreview | null>(null)
const invitePreviewError = ref('')

// 加入方式：邀请码 / 搜索空间
const joinStep = ref<'invite' | 'search'>('invite')
const searchQuery = ref('')
const searchableList = ref<SearchableOrganizationItem[]>([])
const searchLoading = ref(false)
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null

// Tab 内容容器 ref，用于高度过渡
const tabContentWrapperRef = ref<HTMLElement | null>(null)

// 更新容器高度的辅助函数
const updateTabContentHeight = () => {
  if (!tabContentWrapperRef.value) return
  
  // 先移除固定高度，获取自然高度
  tabContentWrapperRef.value.style.height = 'auto'
  const naturalHeight = tabContentWrapperRef.value.scrollHeight
  
  // 设置固定高度以触发过渡
  tabContentWrapperRef.value.style.height = `${naturalHeight}px`
}

// 监听 joinStep 变化，动态调整容器高度以实现平滑过渡
watch(joinStep, () => {
  if (!tabContentWrapperRef.value) return
  
  // 先设置当前高度
  const currentHeight = tabContentWrapperRef.value.scrollHeight
  tabContentWrapperRef.value.style.height = `${currentHeight}px`
  
  // 等待下一帧，让新内容渲染
  requestAnimationFrame(() => {
    updateTabContentHeight()
    
    // 过渡完成后，移除固定高度，让容器自适应
    setTimeout(() => {
      if (tabContentWrapperRef.value) {
        tabContentWrapperRef.value.style.height = 'auto'
      }
    }, 300) // 与 CSS transition 时长一致
  })
}, { flush: 'post' })

// 监听搜索列表变化，更新高度
watch([searchableList, searchLoading], () => {
  if (joinStep.value === 'search') {
    nextTick(() => {
      updateTabContentHeight()
    })
  }
})

// 监听菜单快捷操作事件
const handleOrganizationDialogEvent = ((event: CustomEvent<{ type: 'create' | 'join' }>) => {
  if (event.detail?.type === 'create') {
    // 创建组织使用 SettingsModal
    settingsOrgId.value = ''
    settingsMode.value = 'create'
    showSettingsModal.value = true
  } else if (event.detail?.type === 'join') {
    // 加入组织使用与邀请链接相同的预览弹框，先显示输入邀请码步骤
    joinInputCode.value = ''
    inviteCode.value = ''
    invitePreviewData.value = null
    invitePreviewError.value = ''
    invitePreviewLoading.value = false
    joinStep.value = 'invite'
    searchQuery.value = ''
    searchableList.value = []
    showInvitePreview.value = true
  }
}) as EventListener

// Tab: 'all' | 'created' | 'joined'
const activeTab = ref<'all' | 'created' | 'joined'>('all')

// Computed
const loading = computed(() => orgStore.loading)
const organizations = ref<OrgWithUI[]>([])

const filteredOrganizations = computed(() => {
  if (activeTab.value === 'created') return organizations.value.filter(o => o.is_owner)
  if (activeTab.value === 'joined') return organizations.value.filter(o => !o.is_owner)
  return organizations.value
})

const emptyStateTitle = computed(() => {
  if (activeTab.value === 'created') return t('organization.emptyCreated')
  if (activeTab.value === 'joined') return t('organization.emptyJoined')
  return t('organization.empty')
})

const emptyStateDesc = computed(() => {
  if (activeTab.value === 'created') return t('organization.emptyCreatedDesc')
  if (activeTab.value === 'joined') return t('organization.emptyJoinedDesc')
  return t('organization.emptyDesc')
})

// Watch store changes and update local organizations
watch(
  () => orgStore.organizations,
  (newOrgs) => {
    organizations.value = newOrgs.map(org => ({ ...org, showMore: false }))
  },
  { immediate: true }
)

// Methods
function formatDate(dateStr: string) {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function getRoleTheme(role: string) {
  switch (role) {
    case 'admin': return 'primary'
    case 'editor': return 'warning'
    default: return 'default'
  }
}

const onVisibleChange = (visible: boolean, org: OrgWithUI) => {
  if (!visible) {
    org.showMore = false
  }
}

// 创建组织
function handleCreateOrganization() {
  settingsOrgId.value = ''
  settingsMode.value = 'create'
  showSettingsModal.value = true
}

// 加入组织
function handleJoinOrganization() {
  joinInputCode.value = ''
  inviteCode.value = ''
  invitePreviewData.value = null
  invitePreviewError.value = ''
  invitePreviewLoading.value = false
  joinStep.value = 'invite'
  searchQuery.value = ''
  searchableList.value = []
  showInvitePreview.value = true
}

function handleCardClick(org: OrgWithUI) {
  // 如果弹窗正在显示，不触发设置
  if (org.showMore) {
    return
  }
  settingsOrgId.value = org.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

function handleSettingsSaved() {
  orgStore.fetchOrganizations()
}


function handleSettings(org: OrgWithUI) {
  org.showMore = false
  settingsOrgId.value = org.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

function handleLeave(org: OrgWithUI) {
  org.showMore = false
  leavingOrg.value = org
  leaveVisible.value = true
}

async function confirmLeave() {
  if (!leavingOrg.value) return
  const success = await orgStore.leave(leavingOrg.value.id)
  if (success) {
    MessagePlugin.success(t('organization.leaveSuccess'))
    leaveVisible.value = false
    leavingOrg.value = null
  } else {
    MessagePlugin.error(orgStore.error || t('organization.leaveFailed'))
  }
}

function handleDelete(org: OrgWithUI) {
  org.showMore = false
  deletingOrg.value = org
  deleteVisible.value = true
}

async function confirmDelete() {
  if (!deletingOrg.value) return
  const success = await orgStore.remove(deletingOrg.value.id)
  if (success) {
    MessagePlugin.success(t('organization.deleteSuccess'))
    deleteVisible.value = false
    deletingOrg.value = null
  } else {
    MessagePlugin.error(orgStore.error || t('organization.deleteFailed'))
  }
}

// 处理邀请链接预览
async function handleInvitePreview(code: string) {
  inviteCode.value = code
  invitePreviewLoading.value = true
  invitePreviewError.value = ''
  invitePreviewData.value = null
  showInvitePreview.value = true

  try {
    const result = await previewOrganization(code)
    if (result.success && result.data) {
      invitePreviewData.value = result.data
      // 如果已经是成员，显示提示
      if (result.data.is_already_member) {
        invitePreviewError.value = t('organization.invite.alreadyMember')
      }
    } else {
      invitePreviewError.value = result.message || t('organization.invite.invalidCode')
    }
  } catch (e: any) {
    invitePreviewError.value = e?.message || t('organization.invite.previewFailed')
  } finally {
    invitePreviewLoading.value = false
  }
}

// 确认加入组织（区分直接加入 vs 需要审核，支持邀请码和搜索两种方式）
async function confirmJoinOrganization() {
  if (!invitePreviewData.value || invitePreviewData.value.is_already_member) return
  
  // 如果是通过搜索加入的（没有邀请码），使用搜索加入逻辑
  if (!inviteCode.value && invitePreviewData.value.id) {
    await joinBySearchOrg()
    return
  }
  
  // 原有逻辑：通过邀请码加入
  if (!inviteCode.value) return
  
  inviteJoining.value = true
  try {
    // 需要审核的情况：提交申请（带申请角色与可选说明）
    if (invitePreviewData.value.require_approval) {
      const result = await submitJoinRequest({
        invite_code: inviteCode.value,
        message: inviteRequestMessage.value?.trim() || undefined,
        role: inviteRequestRole.value,
      })
      if (result.success) {
        MessagePlugin.success(t('organization.invite.requestSubmitted'))
        showInvitePreview.value = false
        inviteCode.value = ''
        invitePreviewData.value = null
        // 清除 URL 中的 invite_code 参数
        router.replace({ path: route.path, query: {} })
      } else {
        MessagePlugin.error(result.message || t('organization.invite.requestFailed'))
      }
    } else {
      // 直接加入
      const result = await joinOrganization({ invite_code: inviteCode.value })
      if (result.success) {
        MessagePlugin.success(t('organization.invite.joinSuccess'))
        showInvitePreview.value = false
        inviteCode.value = ''
        invitePreviewData.value = null
        // 清除 URL 中的 invite_code 参数
        router.replace({ path: route.path, query: {} })
        // 刷新组织列表
        orgStore.fetchOrganizations()
      } else {
        MessagePlugin.error(result.message || t('organization.invite.joinFailed'))
      }
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.invite.joinFailed'))
  } finally {
    inviteJoining.value = false
  }
}

// 从输入步骤点击「预览」：用输入的邀请码拉取预览
async function doPreviewFromInput() {
  const code = joinInputCode.value?.trim()
  if (!code) {
    MessagePlugin.warning(t('organization.inviteCodeRequired'))
    return
  }
  invitePreviewError.value = ''
  await handleInvitePreview(code)
}

// 关闭邀请预览弹框
function closeInvitePreview() {
  showInvitePreview.value = false
  inviteCode.value = ''
  joinInputCode.value = ''
  invitePreviewData.value = null
  invitePreviewError.value = ''
  joinStep.value = 'invite'
  searchQuery.value = ''
  searchableList.value = []
  inviteRequestRole.value = 'viewer'
  inviteRequestMessage.value = ''
  router.replace({ path: route.path, query: {} })
}

// 搜索可加入空间
async function doSearchSearchable() {
  searchLoading.value = true
  try {
    const res = await searchSearchableOrganizations(searchQuery.value.trim(), 20)
    if (res.success && res.data) {
      searchableList.value = res.data.data || []
    } else {
      searchableList.value = []
    }
  } catch (e) {
    searchableList.value = []
  } finally {
    searchLoading.value = false
  }
}

function doSearchSearchableDebounced() {
  if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
  searchDebounceTimer = setTimeout(() => doSearchSearchable(), 300)
}

// 预览搜索到的空间（转换为预览格式）
function previewSearchableOrg(org: SearchableOrganizationItem) {
  // 将 SearchableOrganizationItem 转换为 OrganizationPreview 格式
  invitePreviewData.value = {
    id: org.id,
    name: org.name,
    description: org.description,
    avatar: org.avatar,
    member_count: org.member_count,
    share_count: org.share_count,
    is_already_member: org.is_already_member,
    require_approval: org.require_approval,
    created_at: '', // 搜索列表中没有创建时间，使用空字符串
  }
  // 清空邀请码，因为这是通过搜索加入的
  inviteCode.value = ''
}

// 查看搜索到的空间（已是成员时，打开空间设置）
function viewSearchableOrg(org: SearchableOrganizationItem) {
  // 关闭加入弹框
  closeInvitePreview()
  // 打开空间设置弹框
  settingsOrgId.value = org.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

// 从预览弹框中查看空间（已是成员时）
function viewOrganizationFromPreview() {
  if (!invitePreviewData.value) return
  // 关闭加入弹框
  closeInvitePreview()
  // 打开空间设置弹框
  settingsOrgId.value = invitePreviewData.value.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

// 从搜索列表加入空间（通过空间 ID，无需邀请码）- 在预览确认后调用
async function joinBySearchOrg() {
  if (!invitePreviewData.value || invitePreviewData.value.is_already_member) return
  
  inviteJoining.value = true
  try {
    // 如果需要审核，传递角色和消息；否则直接加入
    const message = invitePreviewData.value.require_approval ? inviteRequestMessage.value?.trim() || undefined : undefined
    const role = invitePreviewData.value.require_approval ? inviteRequestRole.value : undefined
    const result = await joinOrganizationById(invitePreviewData.value.id, message, role)
    if (result.success) {
      if (invitePreviewData.value.require_approval) {
        MessagePlugin.success(t('organization.invite.requestSubmitted'))
      } else {
        MessagePlugin.success(t('organization.invite.joinSuccess'))
        orgStore.fetchOrganizations()
      }
      showInvitePreview.value = false
      invitePreviewData.value = null
      searchableList.value = []
      searchQuery.value = ''
      joinStep.value = 'invite'
      inviteRequestRole.value = 'viewer'
      inviteRequestMessage.value = ''
    } else {
      MessagePlugin.error(result.message || t('organization.invite.joinFailed'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('organization.invite.joinFailed'))
  } finally {
    inviteJoining.value = false
  }
}

// Lifecycle
onMounted(async () => {
  orgStore.fetchOrganizations()
  window.addEventListener('openOrganizationDialog', handleOrganizationDialogEvent)
  
  // 检查 URL 中是否有邀请码
  const code = route.query.invite_code as string
  if (code) {
    await handleInvitePreview(code)
  }
})

onUnmounted(() => {
  window.removeEventListener('openOrganizationDialog', handleOrganizationDialogEvent)
})
</script>

<style scoped lang="less">
.org-list-container {
  padding: 24px 44px;
  margin: 0 20px;
  height: calc(100vh);
  overflow-y: auto;
  box-sizing: border-box;
  flex: 1;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  h2 {
    margin: 0;
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 12px;

    .org-join-btn {
      border-color: #07c05f;
      color: #07c05f;
      
      &:hover {
        background-color: rgba(7, 192, 95, 0.08);
        border-color: #05a04f;
        color: #05a04f;
      }
    }

    .org-create-btn {
      background: linear-gradient(135deg, #07c05f 0%, #00a67e 100%);
      border: none;
      color: #fff;

      &:hover {
        background: linear-gradient(135deg, #05a04f 0%, #008a6a 100%);
      }
    }
  }
}

.header-subtitle {
  margin: 0;
  color: #00000099;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

// Tab 切换样式（下划线式，简洁清晰）
.org-tabs {
  display: flex;
  align-items: center;
  gap: 24px;
  border-bottom: 1px solid #e7ebf0;
  margin-bottom: 20px;

  .tab-item {
    padding: 12px 0;
    cursor: pointer;
    color: #666;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    user-select: none;
    position: relative;
    transition: color 0.2s ease;

    &:hover {
      color: #333;
    }

    &.active {
      color: #07c05f;
      font-weight: 500;

      &::after {
        content: '';
        position: absolute;
        bottom: -1px;
        left: 0;
        right: 0;
        height: 2px;
        background: #07c05f;
        border-radius: 1px;
      }
    }
  }
}

.org-card-wrap {
  display: grid;
  gap: 16px;
  grid-template-columns: 1fr;
}

// 已加入标识（底部与角色标签同行）
.pending-requests-badge {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;
  background: rgba(250, 173, 20, 0.12);
  color: #d48806;
  white-space: nowrap;
}

.org-card {
  border: 1px solid #e8ecf1;
  border-radius: 12px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: #fff;
  position: relative;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
  padding: 18px 20px;
  display: flex;
  flex-direction: column;
  height: 160px;

  &.joined-org {
    &:hover {
      border-color: #b8e6c9;
      box-shadow: 0 2px 8px rgba(7, 192, 95, 0.08);
    }
  }

  &:hover {
    border-color: #07c05f;
    box-shadow: 0 2px 10px rgba(7, 192, 95, 0.1);
  }

  .card-decoration {
    color: rgba(7, 192, 95, 0.24);
  }

  &:hover .card-decoration {
    color: rgba(7, 192, 95, 0.38);
  }

  .card-header {
    position: relative;
    z-index: 2;
  }

  .card-content,
  .card-bottom {
    position: relative;
    z-index: 1;
  }
}

// 装饰图标样式
.card-decoration {
  position: absolute;
  top: 10px;
  right: 50px;
  display: flex;
  align-items: flex-start;
  gap: 6px;
  pointer-events: none;
  z-index: 0;
  transition: color 0.25s ease;

  .org-icon {
    opacity: 0.9;
  }

  .connect-icon {
    margin-top: 16px;
    opacity: 0.7;
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
  position: relative;
  z-index: 2;
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}

// 空间头像容器（SpaceAvatar 自带样式）
.org-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.card-title-block {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.card-title {
  color: #1f2937;
  font-family: "PingFang SC";
  font-size: 15px;
  font-weight: 500;
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-subtitle {
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 400;
  color: #9ca3af;
  line-height: 1.3;
}

.more-wrap {
  display: flex;
  width: 28px;
  height: 28px;
  justify-content: center;
  align-items: center;
  border-radius: 6px;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.2s ease;
  opacity: 0;

  .org-card:hover & {
    opacity: 0.6;
  }

  &:hover {
    background: rgba(0, 0, 0, 0.05);
    opacity: 1 !important;
  }

  &.active-more {
    background: rgba(0, 0, 0, 0.06);
    opacity: 1 !important;
  }

  .more-icon {
    width: 16px;
    height: 16px;
  }
}

.card-content {
  flex: 1;
  margin-bottom: 14px;
  overflow: hidden;
}

.card-description {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  color: #8c8c8c;
  font-family: "PingFang SC";
  font-size: 13px;
  font-weight: 400;
  line-height: 1.5;
}

.card-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid #f0f2f5;
}

.bottom-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.feature-badges {
  display: flex;
  align-items: center;
  gap: 6px;
}

.feature-badge {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 26px;
  border-radius: 6px;
  cursor: default;
  transition: background 0.2s ease;

  &.member-badge {
    background: rgba(7, 192, 95, 0.08);
    color: #07c05f;
    padding: 0 8px;
    gap: 4px;

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }

    .badge-count {
      font-size: 12px;
      font-weight: 500;
    }
  }

  &.share-badge {
    background: rgba(0, 82, 217, 0.08);
    color: #0052d9;
    padding: 0 8px;
    gap: 4px;

    &:hover {
      background: rgba(0, 82, 217, 0.12);
    }

    .badge-count {
      font-size: 12px;
      font-weight: 500;
    }
  }

}

.role-tag {
  height: 22px;
  padding: 0 8px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;

  &.owner {
    background: rgba(124, 77, 255, 0.1);
    color: #7c4dff;
    border: none;
  }

  &.admin {
    background: rgba(7, 192, 95, 0.12);
    color: #07c05f;
    border: none;
  }

  &.editor {
    background: rgba(7, 192, 95, 0.08);
    color: #059669;
    border: none;
  }

  &.viewer {
    background: rgba(107, 114, 128, 0.08);
    color: #6b7280;
    border: none;
  }
}

.card-time {
  color: #9ca3af;
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 400;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  padding: 60px 20px;

  .empty-img {
    width: 162px;
    height: 162px;
    margin-bottom: 20px;
  }

  .empty-txt {
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 26px;
    margin-bottom: 8px;
  }

  .empty-desc {
    color: #00000066;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
  }
}

// 响应式布局
@media (min-width: 900px) {
  .org-card-wrap {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (min-width: 1250px) {
  .org-card-wrap {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (min-width: 1600px) {
  .org-card-wrap {
    grid-template-columns: repeat(4, 1fr);
  }
}

// 删除/离开确认对话框样式
:deep(.del-org-dialog) {
  padding: 0px !important;
  border-radius: 6px !important;

  .t-dialog__header {
    display: none;
  }

  .t-dialog__body {
    padding: 16px;
  }

  .t-dialog__footer {
    padding: 0;
  }
}

:deep(.t-dialog__position.t-dialog--top) {
  padding-top: 40vh !important;
}

.circle-wrap {
  .dialog-header {
    display: flex;
    align-items: center;
    margin-bottom: 8px;
  }

  .circle-img {
    width: 20px;
    height: 20px;
    margin-right: 8px;
  }

  .circle-title {
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
  }

  .del-circle-txt {
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    display: inline-block;
    margin-left: 29px;
    margin-bottom: 21px;
  }

  .circle-btn {
    height: 22px;
    width: 100%;
    display: flex;
    justify-content: flex-end;
  }

  .circle-btn-txt {
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    cursor: pointer;

    &:hover {
      opacity: 0.8;
    }
  }

  .confirm {
    color: #FA5151;
    margin-left: 40px;

    &:hover {
      opacity: 0.8;
    }
  }
}
</style>

<style lang="less">
// 更多操作弹窗样式
.card-more-popup {
  z-index: 99 !important;

  .t-popup__content {
    padding: 6px 0 !important;
    margin-top: 6px !important;
    min-width: 140px;
    border-radius: 6px !important;
    box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1) !important;
    border: 1px solid #e7ebf0 !important;
  }
}

.popup-menu {
  display: flex;
  flex-direction: column;
}

.popup-menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  color: #000000e6;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;

  .menu-icon {
    font-size: 16px;
    flex-shrink: 0;
    color: #00000099;
    transition: color 0.2s ease;
  }

  &:hover {
    background: #f7f9fc;

    .menu-icon {
      color: #000000e6;
    }
  }

  &.delete {
    color: #000000e6;

    &:hover {
      background: #fff1f0;
      color: #fa5151;

      .menu-icon {
        color: #fa5151;
      }
    }
  }
}

// 创建对话框样式优化
.create-org-dialog,
.join-org-dialog {
  .t-form-item__label {
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
  }

  .t-input,
  .t-textarea {
    font-family: "PingFang SC";
  }

  .t-button--theme-primary {
    background-color: #3b82f6;
    border-color: #3b82f6;

    &:hover {
      background-color: #2563eb;
      border-color: #2563eb;
    }
  }
}

// 邀请预览弹框 - 参考 FAQ 导入弹窗风格，更紧凑
.invite-preview-overlay {
  position: fixed;
  inset: 0;
  z-index: 2000;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
}

.invite-preview-modal {
  position: relative;
  width: 100%;
  max-width: 480px;
  max-height: 90vh;
  background: #ffffff;
  border-radius: 12px;
  box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.invite-preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24px 24px 16px;
  border-bottom: 1px solid #e5e5e5;
  flex-shrink: 0;
}

.invite-preview-title {
  margin: 0;
  font-family: "PingFang SC";
  font-size: 16px;
  font-weight: 600;
  color: #000000e6;
}

.invite-preview-close {
  position: absolute;
  top: 20px;
  right: 20px;
  width: 32px;
  height: 32px;
  border: none;
  background: #f5f5f5;
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #666;
  transition: all 0.2s ease;
  z-index: 10;

  &:hover {
    background: #e5e5e5;
    color: #000;
  }
}

.invite-preview-body {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 24px;
  min-height: 0;
  max-height: calc(90vh - 140px);

  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: #f5f5f5;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #d0d0d0;
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: #07C05F;
    }
  }
}

.join-modal-tabs {
  display: flex;
  gap: 24px;
  margin-bottom: 20px;
  border-bottom: 1px solid #e5e5e5;

  .join-tab {
    padding: 8px 0;
    cursor: pointer;
    color: #00000099;
    font-size: 14px;
    font-weight: 400;
    user-select: none;
    position: relative;
    transition: color 0.2s ease;
    font-family: "PingFang SC";

    &:hover {
      color: #000000e6;
    }

    &.active {
      color: #07c05f;
      font-weight: 500;

      &::after {
        content: '';
        position: absolute;
        bottom: -1px;
        left: 0;
        right: 0;
        height: 2px;
        background: #07c05f;
        border-radius: 1px;
      }
    }
  }
}

// Tab 内容容器 - 平滑高度过渡
.join-tab-content-wrapper {
  transition: height 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  overflow: hidden;
}

.join-tab-content {
  width: 100%;
}

.search-input-wrap {
  margin-bottom: 12px;
}

.searchable-list-wrap {
  max-height: 300px;
  overflow-y: auto;
  margin-bottom: 20px;
  border: 1px solid #e5e5e5;
  border-radius: 8px;
  background: #fafafa;

  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: #f5f5f5;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #d0d0d0;
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: #07C05F;
    }
  }
}

.searchable-empty {
  padding: 40px 16px;
  text-align: center;
  color: #00000066;
  font-size: 14px;
  font-family: "PingFang SC";
}

.searchable-list {
  display: flex;
  flex-direction: column;
  gap: 0;

  .searchable-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 16px;
    border-bottom: 1px solid #e5e5e5;
    background: #fff;
    transition: background 0.2s;
    cursor: pointer;

    &:last-child {
      border-bottom: none;
    }

    &:hover:not(.is-already-member) {
      background: #f5f5f5;
    }

    &.is-already-member {
      cursor: default;
      opacity: 0.6;
    }

    .searchable-item-left {
      display: flex;
      align-items: center;
      gap: 12px;
      flex: 1;
      min-width: 0;
    }

    .searchable-item-avatar {
      flex-shrink: 0;
    }

    .searchable-item-info {
      flex: 1;
      min-width: 0;

      .searchable-item-name {
        display: block;
        font-size: 14px;
        font-weight: 500;
        color: #000000e6;
        margin-bottom: 4px;
        font-family: "PingFang SC";
      }

      .searchable-item-desc {
        display: block;
        font-size: 13px;
        color: #00000066;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        margin-bottom: 8px;
        font-family: "PingFang SC";
      }

      .searchable-item-badges {
        display: flex;
        align-items: center;
        gap: 12px;
        flex-wrap: wrap;

        .badge {
          font-size: 12px;
          color: #00000066;
          font-family: "PingFang SC";
        }

        .approval-tag {
          flex-shrink: 0;
        }
      }
    }

    .searchable-item-action {
      flex-shrink: 0;
    }
  }
}

.invite-preview-input {
  .invite-preview-input-desc {
    font-size: 14px;
    color: #333333;
    margin: 0 0 16px;
    line-height: 1.5;
    font-family: "PingFang SC";
  }
  .invite-preview-input-wrap {
    margin-bottom: 10px;
  }
  .invite-preview-input-tip {
    font-size: 12px;
    color: #00000066;
    margin: 0 0 20px;
    line-height: 1.4;
    font-family: "PingFang SC";
  }
  .invite-preview-error-inline {
    display: flex;
    align-items: center;
    gap: 8px;
    color: #FA5151;
    font-size: 13px;
    margin-bottom: 16px;
    font-family: "PingFang SC";
  }
  .invite-preview-footer-single {
    margin: 20px 0 0;
    padding: 0;
    border-top: none;
    background: transparent;
  }
}

.invite-preview-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 56px 28px;
  gap: 16px;

  .invite-preview-loading-text {
    font-size: 14px;
    color: #86909c;
    font-family: "PingFang SC";
  }
}

.invite-preview-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 40px 28px;

  .invite-preview-error-icon {
    color: #e34d59;
    margin-bottom: 20px;
  }

  .invite-preview-error-title {
    font-size: 18px;
    font-weight: 600;
    color: #1f2937;
    margin: 0 0 8px;
    font-family: "PingFang SC";
  }

  .invite-preview-error-desc {
    font-size: 14px;
    color: #86909c;
    margin: 0 0 24px;
    line-height: 1.5;
    font-family: "PingFang SC";
  }
}

// 预览内容区域 - 紧凑布局
.invite-preview-org {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 20px;
}

.invite-preview-org-icon {
  flex-shrink: 0;
}

.invite-preview-org-info {
  flex: 1;
  min-width: 0;
}

.invite-preview-org-name {
  font-size: 16px;
  font-weight: 600;
  color: #000000e6;
  margin: 0 0 6px;
  font-family: "PingFang SC";
  line-height: 1.3;
}

.invite-preview-org-desc {
  font-size: 14px;
  color: #00000066;
  margin: 0;
  line-height: 1.5;
  font-family: "PingFang SC";
}

.invite-preview-stats {
  display: flex;
  gap: 24px;
  margin-bottom: 20px;
  padding-bottom: 20px;
  border-bottom: 1px solid #e5e5e5;
}

.invite-preview-stat {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: #333333;
  font-family: "PingFang SC";

  .invite-preview-stat-icon {
    color: #07c05f;
    font-size: 16px;
  }

  .invite-preview-stat-value {
    font-weight: 600;
    color: #000000e6;
  }

  .invite-preview-stat-label {
    color: #00000066;
    font-weight: 400;
  }
}

.invite-preview-join-section,
.invite-preview-status-section {
  margin-top: 20px;
}

.invite-preview-approval-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 16px;
  font-size: 14px;
  font-family: "PingFang SC";

  .invite-preview-approval-label {
    color: #333333;
    flex-shrink: 0;
    font-weight: 500;
  }

  .invite-preview-approval-badge {
    display: inline-flex;
    align-items: center;
    padding: 4px 10px;
    border-radius: 6px;
    font-size: 12px;
    font-weight: 500;

    &.need-approval {
      background: rgba(250, 173, 20, 0.12);
      color: #d48806;
    }

    &.no-approval {
      background: rgba(7, 192, 95, 0.12);
      color: #07c05f;
    }
  }
}

.invite-preview-tip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 6px;
  font-size: 13px;
  line-height: 1.5;
  font-family: "PingFang SC";
  margin-bottom: 16px;

  .t-icon {
    flex-shrink: 0;
    margin-top: 1px;
  }
}

.invite-preview-tip-success {
  background: rgba(7, 192, 95, 0.08);
  color: #07c05f;
}

.invite-preview-tip-warning {
  background: rgba(250, 173, 20, 0.08);
  color: #d48806;
}

.invite-preview-tip-info {
  background: rgba(0, 112, 240, 0.06);
  color: #0052d9;
}

.invite-preview-form-group {
  margin-bottom: 20px;

  &:last-child {
    margin-bottom: 0;
  }
}

.invite-preview-form-label {
  display: block;
  margin-bottom: 8px;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #333333;
  letter-spacing: -0.2px;
}

.invite-preview-role-select {
  width: 100%;
  max-width: 180px;
}

.invite-preview-message-input {
  width: 100%;
}

.invite-preview-footer {
  padding: 16px 24px;
  border-top: 1px solid #e5e5e5;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
}

.modal-enter-active,
.modal-leave-active {
  transition: all 0.3s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .invite-preview-modal {
    transform: scale(0.95);
  }
}
</style>
