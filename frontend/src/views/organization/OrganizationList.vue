<template>
  <div class="org-list-container">
    <!-- 头部：仅标题与副标题 -->
    <div class="header">
      <div class="header-title">
        <h2>{{ $t('organization.title') }}</h2>
        <p class="header-subtitle">{{ $t('organization.subtitle') }}</p>
      </div>
    </div>

    <!-- 左侧筛选 + 主内容 -->
    <div class="org-list-body">
      <ListSpaceSidebar
        mode="organization"
        v-model="spaceSelection"
        :count-all="organizations.length"
        :count-created="createdCount"
        :count-joined="joinedCount"
      >
        <template #actions>
          <t-tooltip :content="$t('organization.joinOrg')" placement="top">
            <t-button
              variant="text"
              theme="default"
              class="sidebar-action-btn"
              size="small"
              :aria-label="$t('organization.joinOrg')"
              @click="handleJoinOrganization"
            >
              <template #icon><t-icon name="enter" size="16px" /></template>
            </t-button>
          </t-tooltip>
          <t-tooltip :content="$t('organization.createOrg')" placement="top">
            <t-button
              variant="text"
              theme="default"
              class="sidebar-action-btn"
              size="small"
              :aria-label="$t('organization.createOrg')"
              @click="handleCreateOrganization"
            >
              <template #icon><img src="@/assets/img/organization-green.svg" class="org-create-icon sidebar-org-icon" alt="" aria-hidden="true" /></template>
            </t-button>
          </t-tooltip>
        </template>
      </ListSpaceSidebar>
      <div class="org-list-main">
    <!-- 卡片网格 -->
    <div v-if="filteredOrganizations.length > 0" class="org-card-wrap">
      <div
        v-for="(org, index) in filteredOrganizations"
        :key="org.id"
        class="org-card"
        :class="{ 'joined-org': !org.is_owner }"
        @click="handleCardClick(org)"
      >
        <!-- 装饰：协作网络感图形 -->
        <div class="card-decoration">
          <svg class="card-deco-svg" width="56" height="40" viewBox="0 0 56 40" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
            <circle cx="10" cy="12" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5"/>
            <circle cx="28" cy="8" r="5" stroke="currentColor" stroke-width="1.8" fill="none" opacity="0.7"/>
            <circle cx="46" cy="14" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5"/>
            <path d="M14 13 L24 10 M32 10 L42 13" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" opacity="0.4"/>
            <circle cx="28" cy="28" r="6" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.35"/>
            <path d="M28 14 L28 22 M20 18 L26 24 M36 18 L30 24" stroke="currentColor" stroke-width="1" stroke-linecap="round" opacity="0.3"/>
          </svg>
        </div>

        <!-- 卡片头部 -->
        <div class="card-header">
          <div class="card-header-left">
            <div class="org-avatar">
              <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
            </div>
            <div class="card-title-block">
              <span class="card-title" :title="org.name">{{ org.name }}</span>
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

        <!-- 卡片底部（与知识库卡片风格统一：小标签、无日期、智能体用主题色） -->
        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badges">
              <t-tooltip :content="$t('organization.memberCount')" placement="top">
                <div class="feature-badge stat-member">
                  <t-icon name="user" size="14px" />
                  <span class="badge-count">{{ org.member_count || 0 }}</span>
                </div>
              </t-tooltip>
              <t-tooltip :content="$t('organization.invite.knowledgeBases')" placement="top">
                <div class="feature-badge stat-kb">
                  <t-icon name="folder" size="14px" />
                  <span class="badge-count">{{ org.share_count ?? 0 }}</span>
                </div>
              </t-tooltip>
              <t-tooltip :content="$t('organization.invite.agents')" placement="top">
                <div class="feature-badge stat-agent">
                  <img src="@/assets/img/agent-green.svg" class="stat-agent-icon" alt="" aria-hidden="true" />
                  <span class="badge-count">{{ org.agent_share_count ?? 0 }}</span>
                </div>
              </t-tooltip>
            </div>
            <t-tooltip v-if="(org.pending_join_request_count ?? 0) > 0" :content="$t('organization.settings.pendingJoinRequestsBadge')" placement="top">
              <span class="pending-requests-badge">{{ org.pending_join_request_count }} {{ $t('organization.settings.pendingReview') }}</span>
            </t-tooltip>
          </div>
          <div class="bottom-right">
            <div class="relation-role-tag" :class="org.is_owner ? 'owner' : (org.my_role || '')">
              <t-icon :name="org.is_owner ? 'usergroup-add' : 'usergroup'" size="14px" />
              <span>{{ org.is_owner ? $t('organization.owner') : (org.my_role ? $t(`organization.role.${org.my_role}`) : $t('organization.joinedByMe')) }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态（按筛选显示不同文案） -->
    <div v-else-if="!loading" class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="">
      <span class="empty-txt">{{ emptyStateTitle }}</span>
      <span class="empty-desc">{{ emptyStateDesc }}</span>
      <div class="empty-state-actions">
        <t-button theme="default" variant="outline" class="org-join-btn" @click="handleJoinOrganization">
          <template #icon><t-icon name="enter" /></template>
          {{ $t('organization.joinOrg') }}
        </t-button>
        <t-button class="org-create-btn" @click="handleCreateOrganization">
          <template #icon><img src="@/assets/img/organization-green.svg" class="org-create-icon" alt="" aria-hidden="true" /></template>
          {{ $t('organization.createOrg') }}
        </t-button>
      </div>
    </div>
      </div>
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
              <!-- 预览详情且来自搜索时显示返回按钮 -->
              <button
                v-if="invitePreviewData && !inviteCode"
                class="invite-preview-back"
                @click="backFromPreview"
                :aria-label="$t('organization.join.backToSearch')"
              >
                <t-icon name="chevron-left" />
              </button>
              <h2 class="invite-preview-title">{{ invitePreviewData ? $t('organization.invite.previewTitle') : $t('organization.joinOrg') }}</h2>
              <button class="invite-preview-close" @click="closeInvitePreview" :aria-label="$t('common.close')">
                <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                  <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
                </svg>
              </button>
            </div>

            <!-- 步骤1/2/Loading 共用高度过渡容器 -->
            <div class="invite-preview-body-wrap" :style="inviteBodyWrapStyle">
              <div ref="inviteBodyInnerRef" class="invite-body-inner">
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
                  @click="handleSearchTabClick"
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

                <!-- 搜索可加入空间（与主列表卡片风格一致） -->
                <div v-else-if="joinStep === 'search'" class="join-tab-content join-tab-search">
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
                        <img class="searchable-empty-img" src="@/assets/img/upload.svg" alt="">
                        <span class="searchable-empty-txt">
                          {{ searchQuery ? $t('organization.join.noSearchResult') : $t('organization.join.noSearchableSpaces') }}
                        </span>
                      </div>
                      <div v-else class="searchable-list">
                        <div
                          v-for="org in searchableList"
                          :key="org.id"
                          class="searchable-card"
                          :class="{ 'is-full': isOrgFull(org) }"
                          @click="!isOrgFull(org) && previewSearchableOrg(org)"
                        >
                          <div class="searchable-card-decoration">
                            <svg class="searchable-card-deco-svg" width="40" height="28" viewBox="0 0 56 40" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
                              <circle cx="8" cy="10" r="3" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.5"/>
                              <circle cx="22" cy="6" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.6"/>
                              <circle cx="36" cy="10" r="3" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.5"/>
                              <path d="M11 10 L18 8 M26 8 L33 10" stroke="currentColor" stroke-width="1" stroke-linecap="round" opacity="0.4"/>
                            </svg>
                          </div>
                          <div class="searchable-card-header">
                            <div class="searchable-card-header-left">
                              <div class="searchable-card-avatar">
                                <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
                              </div>
                              <span class="searchable-card-title" :title="org.name">{{ org.name }}</span>
                            </div>
                            <div class="searchable-card-action" @click.stop>
                              <t-button
                                v-if="isOrgFull(org)"
                                theme="default"
                                variant="outline"
                                size="small"
                                disabled
                              >
                                {{ $t('organization.join.memberLimitReached') }}
                              </t-button>
                              <t-button
                                v-else
                                theme="primary"
                                variant="base"
                                size="small"
                                @click="previewSearchableOrg(org)"
                              >
                                {{ $t('organization.invite.previewAction') }}
                              </t-button>
                            </div>
                          </div>
                          <div class="searchable-card-content">
                            <p class="searchable-card-desc">{{ org.description || $t('organization.noDescription') }}</p>
                          </div>
                          <div class="searchable-card-bottom">
                            <div class="searchable-card-badges">
                              <span class="searchable-badge member">
                                <t-icon name="user" size="12px" />
                                <template v-if="org.member_limit > 0">
                                  {{ org.member_count }}/{{ org.member_limit }}
                                </template>
                                <template v-else>{{ org.member_count }}</template>
                              </span>
                              <span class="searchable-badge share">
                                <t-icon name="folder" size="12px" />
                                {{ org.share_count }}
                              </span>
                              <span class="searchable-badge searchable-badge-agent">
                                <img src="@/assets/img/agent.svg" class="searchable-badge-agent-icon" alt="" aria-hidden="true" />
                                {{ org.agent_share_count ?? 0 }}
                              </span>
                              <t-tag v-if="org.require_approval" class="searchable-tag-approval" size="small" variant="light">
                                {{ $t('organization.invite.needApproval') }}
                              </t-tag>
                              <t-tag v-if="isOrgFull(org)" class="searchable-tag-full" size="small" variant="light">
                                {{ $t('organization.join.memberLimitReached') }}
                              </t-tag>
                            </div>
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

            <!-- 步骤2：空间详情预览（与主列表卡片风格一致） -->
            <div v-else-if="invitePreviewData" class="invite-preview-body invite-preview-body-preview">
                <!-- 空间信息卡片（与 org-card / searchable-card 一致） -->
                <div class="preview-detail-card">
                  <div class="preview-detail-decoration">
                    <svg class="preview-detail-deco-svg" width="56" height="40" viewBox="0 0 56 40" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
                      <circle cx="10" cy="12" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5"/>
                      <circle cx="28" cy="8" r="5" stroke="currentColor" stroke-width="1.8" fill="none" opacity="0.7"/>
                      <circle cx="46" cy="14" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5"/>
                      <path d="M14 13 L24 10 M32 10 L42 13" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" opacity="0.4"/>
                      <circle cx="28" cy="28" r="6" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.35"/>
                      <path d="M28 14 L28 22 M20 18 L26 24 M36 18 L30 24" stroke="currentColor" stroke-width="1" stroke-linecap="round" opacity="0.3"/>
                    </svg>
                  </div>
                  <div class="preview-detail-header">
                    <div class="preview-detail-header-left">
                      <div class="preview-detail-avatar">
                        <SpaceAvatar :name="invitePreviewData.name" :avatar="invitePreviewData.avatar" size="large" />
                      </div>
                      <div class="preview-detail-title-block">
                        <h2 class="preview-detail-name">{{ invitePreviewData.name }}</h2>
                        <div class="preview-detail-id-row">
                          <span class="preview-detail-id-label">{{ $t('organization.join.spaceId') }}</span>
                          <span class="preview-detail-id-value">{{ shortPreviewSpaceId }}</span>
                          <t-tooltip :content="$t('common.copy')">
                            <t-button variant="text" size="small" class="preview-detail-id-copy" @click="copyPreviewSpaceId">
                              <t-icon name="file-copy" />
                            </t-button>
                          </t-tooltip>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div class="preview-detail-content">
                    <p class="preview-detail-desc">{{ invitePreviewData.description || $t('organization.noDescription') }}</p>
                  </div>
                  <div class="preview-detail-bottom">
                    <div class="preview-detail-badges">
                      <span class="preview-badge member">
                        <t-icon name="user" size="14px" />
                        {{ invitePreviewData.member_count }} {{ $t('organization.invite.members') }}
                      </span>
                      <span class="preview-badge share">
                        <t-icon name="folder" size="14px" />
                        {{ invitePreviewData.share_count }} {{ $t('organization.invite.knowledgeBases') }}
                      </span>
                      <span class="preview-badge preview-badge-agent">
                        <img src="@/assets/img/agent.svg" class="preview-badge-agent-icon" alt="" aria-hidden="true" />
                        {{ invitePreviewData.agent_share_count ?? 0 }} {{ $t('organization.invite.agents') }}
                      </span>
                      <t-tag v-if="invitePreviewData.require_approval" class="preview-tag-approval" size="small" variant="light">
                        {{ $t('organization.invite.needApproval') }}
                      </t-tag>
                    </div>
                  </div>
                </div>

                <!-- 加入方式与说明（紧凑面板） -->
                <div v-if="!invitePreviewData.is_already_member" class="preview-join-section">
                  <div class="preview-join-row">
                    <span class="preview-join-label">{{ $t('organization.invite.approvalLabel') }}</span>
                    <span :class="['preview-join-value', invitePreviewData.require_approval ? 'value-warning' : 'value-success']">
                      {{ invitePreviewData.require_approval ? $t('organization.invite.needApproval') : $t('organization.invite.noApproval') }}
                    </span>
                  </div>
                  <div v-if="!invitePreviewData.require_approval" class="preview-join-note">
                    {{ $t('organization.invite.defaultRoleAfterJoin', { role: $t('organization.role.viewer') }) }}
                  </div>
                  <template v-else>
                    <div class="preview-join-note preview-join-note-warning">
                      {{ $t('organization.invite.requireApprovalTip') }}
                    </div>
                    <div class="preview-form-group">
                      <label class="preview-form-label">{{ $t('organization.invite.requestRole') }}</label>
                      <t-select
                        v-model="inviteRequestRole"
                        class="preview-role-select"
                        size="medium"
                        :placeholder="$t('organization.invite.selectRole')"
                        :options="orgRoleOptions"
                      />
                    </div>
                    <div class="preview-form-group">
                      <label class="preview-form-label">{{ $t('organization.invite.applicationNote') }}</label>
                      <t-textarea
                        v-model="inviteRequestMessage"
                        class="preview-message-input"
                        size="medium"
                        :placeholder="$t('organization.invite.messagePlaceholder')"
                        :maxlength="500"
                        :autosize="{ minRows: 2, maxRows: 4 }"
                      />
                    </div>
                  </template>
                </div>

                <div v-if="invitePreviewData.is_already_member" class="preview-status-section">
                  <div class="preview-join-note preview-join-note-success">
                    <t-icon name="check-circle" size="16px" />
                    {{ $t('organization.invite.alreadyMember') }}
                  </div>
                </div>

                <div class="invite-preview-footer">
                  <t-button theme="default" variant="outline" size="medium" @click="backFromPreview">
                    {{ !inviteCode ? $t('organization.join.backToSearch') : $t('common.cancel') }}
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
            </div>
              </div>
            </div>
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
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'

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
// 搜索结果缓存：避免重复点击时重复请求导致高度跳动
const searchCache = ref<{ query: string; data: SearchableOrganizationItem[]; timestamp: number } | null>(null)
const CACHE_DURATION = 5 * 60 * 1000 // 缓存5分钟

// Tab 内容容器 ref，用于高度过渡
const tabContentWrapperRef = ref<HTMLElement | null>(null)

// 加入弹框整体 body 高度过渡（输入邀请码 / 搜索空间 / 查看详情）
const inviteBodyInnerRef = ref<HTMLElement | null>(null)
const inviteBodyHeightPx = ref<number>(0)
let inviteBodyResizeObserver: ResizeObserver | null = null

const inviteBodyWrapStyle = computed(() => {
  const px = inviteBodyHeightPx.value
  if (px <= 0) return {}
  return { maxHeight: `${px}px`, minHeight: `${px}px` }
})

// 预览中空间 ID 的简短显示（前 8 位 + …）
const shortPreviewSpaceId = computed(() => {
  const id = invitePreviewData.value?.id
  if (!id) return ''
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
})

// 根据当前 body 内容更新高度（用于过渡动画）
function updateInviteBodyHeight() {
  const el = inviteBodyInnerRef.value
  if (!el || !showInvitePreview.value) return
  const h = el.scrollHeight
  // 避免把高度写成 0 导致闪缩，仅在得到有效高度时更新
  if (h > 0) inviteBodyHeightPx.value = h
}

// 观察加入弹框 body 内容高度，用于步骤切换时的高度过渡动画
function setupInviteBodyResizeObserver() {
  if (inviteBodyResizeObserver) return
  const el = inviteBodyInnerRef.value
  if (!el || !showInvitePreview.value) return
  inviteBodyResizeObserver = new ResizeObserver((entries) => {
    const entry = entries[0]
    if (!entry) return
    const h = entry.contentRect.height
    // 避免切换瞬间读到 0 导致闪缩
    if (h > 0 || inviteBodyHeightPx.value <= 0) inviteBodyHeightPx.value = h
  })
  inviteBodyResizeObserver.observe(el)
  inviteBodyHeightPx.value = el.scrollHeight
}

function teardownInviteBodyResizeObserver() {
  if (inviteBodyResizeObserver) {
    inviteBodyResizeObserver.disconnect()
    inviteBodyResizeObserver = null
  }
  inviteBodyHeightPx.value = 0
}

watch(
  [showInvitePreview, inviteBodyInnerRef],
  ([show, inner]) => {
    if (!show) {
      teardownInviteBodyResizeObserver()
      return
    }
    if (inner) {
      nextTick(() => {
        setupInviteBodyResizeObserver()
      })
    }
  },
  { flush: 'post' }
)

// 步骤切换时在布局完成后读取新内容高度，保证高度过渡动画可见
watch(
  [() => invitePreviewLoading.value, () => invitePreviewData.value],
  () => {
    if (!showInvitePreview.value || !inviteBodyInnerRef.value) return
    nextTick(() => {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          updateInviteBodyHeight()
        })
      })
    })
  },
  { flush: 'post' }
)

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
    // 注意：不清空缓存，保留搜索结果以便下次快速显示
    showInvitePreview.value = true
  }
}) as EventListener

// 左侧筛选：'all' | 'created' | 'joined'
const spaceSelection = ref<'all' | 'created' | 'joined'>('all')

// Computed
const loading = computed(() => orgStore.loading)
const organizations = ref<OrgWithUI[]>([])

const createdCount = computed(() => organizations.value.filter(o => o.is_owner).length)
const joinedCount = computed(() => organizations.value.filter(o => !o.is_owner).length)

const filteredOrganizations = computed(() => {
  if (spaceSelection.value === 'created') return organizations.value.filter(o => o.is_owner)
  if (spaceSelection.value === 'joined') return organizations.value.filter(o => !o.is_owner)
  return organizations.value
})

const emptyStateTitle = computed(() => {
  if (spaceSelection.value === 'created') return t('organization.emptyCreated')
  if (spaceSelection.value === 'joined') return t('organization.emptyJoined')
  return t('organization.empty')
})

const emptyStateDesc = computed(() => {
  if (spaceSelection.value === 'created') return t('organization.emptyCreatedDesc')
  if (spaceSelection.value === 'joined') return t('organization.emptyJoinedDesc')
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

// 从预览详情返回：若来自搜索则回到搜索 Tab，否则回到步骤 1
function backFromPreview() {
  const fromSearch = !inviteCode.value
  invitePreviewData.value = null
  inviteRequestRole.value = 'viewer'
  inviteRequestMessage.value = ''
  if (fromSearch) {
    joinStep.value = 'search'
  }
}

// 处理搜索标签点击：如果有缓存，先显示缓存，避免高度跳动
function handleSearchTabClick() {
  joinStep.value = 'search'
  
  // 检查是否有有效的缓存
  const currentQuery = searchQuery.value.trim()
  if (searchCache.value && 
      searchCache.value.query === currentQuery &&
      Date.now() - searchCache.value.timestamp < CACHE_DURATION) {
    // 先显示缓存结果（已过滤已加入空间），避免高度跳动
    searchableList.value = searchCache.value.data
    // 然后在后台刷新（可选，如果需要最新数据）
    // doSearchSearchable()
  } else {
    // 没有缓存或缓存过期，执行搜索
    doSearchSearchable()
  }
}

// 搜索可加入空间
async function doSearchSearchable() {
  const currentQuery = searchQuery.value.trim()
  
  // 检查缓存
  if (searchCache.value && 
      searchCache.value.query === currentQuery &&
      Date.now() - searchCache.value.timestamp < CACHE_DURATION) {
    // 使用缓存（已是过滤后的列表），不重新请求
    searchableList.value = searchCache.value.data
    return
  }
  
  searchLoading.value = true
  try {
    const res = await searchSearchableOrganizations(currentQuery, 20)
    if (res.success && res.data) {
      const raw = res.data.data || []
      // 不展示已加入的空间
      const data = raw.filter((org: SearchableOrganizationItem) => !org.is_already_member)
      searchableList.value = data
      // 更新缓存（存过滤后的列表）
      searchCache.value = {
        query: currentQuery,
        data: data,
        timestamp: Date.now()
      }
    } else {
      searchableList.value = []
      // 清空缓存
      searchCache.value = null
    }
  } catch (e) {
    searchableList.value = []
    searchCache.value = null
  } finally {
    searchLoading.value = false
  }
}

function doSearchSearchableDebounced() {
  if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
  searchDebounceTimer = setTimeout(() => doSearchSearchable(), 300)
}

// 空间是否已满（超过成员上限无法加入）
function isOrgFull(org: SearchableOrganizationItem): boolean {
  return org.member_limit > 0 && org.member_count >= org.member_limit
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
    agent_share_count: org.agent_share_count ?? 0,
    is_already_member: org.is_already_member,
    require_approval: org.require_approval,
    created_at: '', // 搜索列表中没有创建时间，使用空字符串
  }
  // 清空邀请码，因为这是通过搜索加入的
  inviteCode.value = ''
}

// 查看搜索到的空间（已是成员时，打开空间设置；不关闭加入弹窗，关闭设置后仍回到搜索）
function viewSearchableOrg(org: SearchableOrganizationItem) {
  settingsOrgId.value = org.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

// 从预览弹框中查看空间（已是成员时；不关闭加入弹窗，关闭设置后仍回到搜索）
function viewOrganizationFromPreview() {
  if (!invitePreviewData.value) return
  settingsOrgId.value = invitePreviewData.value.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

// 复制预览中的空间 ID
function copyPreviewSpaceId() {
  if (!invitePreviewData.value?.id) return
  const text = invitePreviewData.value.id
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(() => {
        MessagePlugin.success(t('common.copied'))
      }).catch(() => {
        fallbackCopyText(text)
        MessagePlugin.success(t('common.copied'))
      })
    } else {
      fallbackCopyText(text)
      MessagePlugin.success(t('common.copied'))
    }
  } catch {
    MessagePlugin.error('复制失败')
  }
}

function fallbackCopyText(text: string) {
  const textArea = document.createElement('textarea')
  textArea.value = text
  textArea.style.position = 'fixed'
  textArea.style.opacity = '0'
  document.body.appendChild(textArea)
  textArea.select()
  document.execCommand('copy')
  document.body.removeChild(textArea)
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
  
  // 检查 URL 中是否有 orgId，如果有则打开空间设置
  const orgId = route.query.orgId as string
  if (orgId) {
    settingsOrgId.value = orgId
    settingsMode.value = 'edit'
    showSettingsModal.value = true
    // 清除 URL 中的 orgId 参数，避免刷新时重复打开
    const newQuery = { ...route.query }
    delete newQuery.orgId
    router.replace({ path: route.path, query: newQuery })
  }
})

onUnmounted(() => {
  window.removeEventListener('openOrganizationDialog', handleOrganizationDialogEvent)
  teardownInviteBodyResizeObserver()
})
</script>

<style scoped lang="less">
.org-list-container {
  padding: 24px 32px;
  margin: 0 16px 0 4px;
  height: calc(100vh);
  box-sizing: border-box;
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.org-list-body {
  display: flex;
  flex: 1;
  min-height: 0;
  background: #fafbfc;
  border: 1px solid #e7ebf0;
  border-radius: 10px;
  overflow: hidden;
}

.org-list-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 12px;
  background: #fafbfc;
}

.header {
  display: flex;
  align-items: center;
  margin-bottom: 20px;
  flex-shrink: 0;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  h2 {
    margin: 0;
    color: #000000e6;
    font-family: "PingFang SC", system-ui, sans-serif;
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }
}

.org-join-btn {
  border-color: rgba(7, 192, 95, 0.5);
  color: #07c05f;
  font-weight: 500;
  transition: all 0.2s ease;

  .t-icon {
    color: #07c05f;
  }

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    border-color: #07c05f;
    color: #07c05f;

    .t-icon {
      color: #07c05f;
    }
  }
}

.org-create-btn {
  background: #07c05f;
  border: none;
  color: #fff;
  font-weight: 500;
  box-shadow: 0 2px 8px rgba(7, 192, 95, 0.25);
  transition: all 0.25s ease;

  &:hover {
    background: #05a650;
    box-shadow: 0 4px 14px rgba(7, 192, 95, 0.35);
  }

  .org-create-icon {
    width: 16px;
    height: 16px;
    filter: brightness(0) invert(1);
  }
}

.header-subtitle {
  margin: 0;
  color: #64748b;
  font-family: "PingFang SC", system-ui, sans-serif;
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

// Tab 切换样式（下划线式，与整体协作感一致）
.org-tabs {
  display: flex;
  align-items: center;
  gap: 28px;
  border-bottom: 1px solid #e2e8f0;
  margin-bottom: 24px;

  .tab-item {
    padding: 12px 0;
    cursor: pointer;
    color: #64748b;
    font-family: "PingFang SC", system-ui, sans-serif;
    font-size: 14px;
    font-weight: 400;
    user-select: none;
    position: relative;
    transition: color 0.2s ease;

    &:hover {
      color: #334155;
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
  gap: 20px;
  grid-template-columns: 1fr;
}

/* 与知识库列表卡片统一尺寸：160px 高、18px 20px 内边距、12px 圆角 */
.org-card {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: linear-gradient(145deg, #ffffff 0%, #f8fafc 100%);
  position: relative;
  cursor: pointer;
  transition: border-color 0.25s ease, box-shadow 0.25s ease, transform 0.2s ease;
  padding: 18px 20px;
  display: flex;
  flex-direction: column;
  height: 160px;
  min-height: 160px;

  &::before {
    content: '';
    position: absolute;
    top: 0;
    right: 0;
    width: 120px;
    height: 80px;
    background: radial-gradient(ellipse 60% 50% at 100% 0%, rgba(7, 192, 95, 0.06) 0%, transparent 70%);
    pointer-events: none;
    z-index: 0;
  }

  &.joined-org {
    &:hover {
      border-color: rgba(7, 192, 95, 0.4);
      box-shadow: 0 4px 16px rgba(7, 192, 95, 0.08);
    }
  }

  &:hover {
    border-color: rgba(7, 192, 95, 0.5);
    box-shadow: 0 6px 20px rgba(7, 192, 95, 0.12);
  }

  .card-decoration {
    color: rgba(7, 192, 95, 0.35);
  }

  &:hover .card-decoration {
    color: rgba(7, 192, 95, 0.55);
  }

  .card-header {
    position: relative;
    z-index: 2;
    margin-bottom: 10px;
  }

  .card-title {
    font-size: 16px;
    line-height: 24px;
  }

  .card-content {
    position: relative;
    z-index: 1;
    margin-bottom: 8px;
  }

  .card-bottom {
    position: relative;
    z-index: 1;
    padding-top: 8px;
  }

  .card-description {
    font-size: 12px;
    line-height: 18px;
  }

  .more-wrap {
    width: 28px;
    height: 28px;
    border-radius: 8px;

    .more-icon {
      width: 16px;
      height: 16px;
    }
  }
}

// 卡片装饰：协作网络图形
.card-decoration {
  position: absolute;
  top: 8px;
  right: 14px;
  display: flex;
  align-items: flex-start;
  justify-content: flex-end;
  pointer-events: none;
  z-index: 0;
  transition: color 0.3s ease;

  .card-deco-svg {
    display: block;
    width: 56px;
    height: 40px;
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  position: relative;
  z-index: 2;
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 8px;
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
  color: #1d2129;
  font-family: "PingFang SC", -apple-system, sans-serif;
  font-size: 15px;
  font-weight: 600;
  line-height: 22px;
  letter-spacing: 0.01em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.more-wrap {
  display: flex;
  width: 28px;
  height: 28px;
  justify-content: center;
  align-items: center;
  border-radius: 8px;
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

/* 与知识库卡片内容区一致 */
.card-content {
  flex: 1;
  min-height: 0;
  margin-bottom: 8px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* 三个列表卡片统一：描述字体 */
.card-description {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  color: #666;
  font-family: "PingFang SC", -apple-system, sans-serif;
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.card-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid #f0f0f0;
}

.bottom-left {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
}

// 与知识库卡片统一的底部标签：小尺寸、统一圆角
.feature-badges {
  display: flex;
  align-items: center;
  gap: 4px;
}

.feature-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  height: 20px;
  padding: 0 5px;
  border-radius: 5px;
  font-size: 11px;
  font-weight: 500;
  font-family: "PingFang SC", system-ui, sans-serif;
  cursor: default;
  transition: background 0.2s ease;

  .t-icon {
    flex-shrink: 0;
  }

  .badge-count {
    line-height: 1;
  }

  &.stat-member {
    background: rgba(100, 116, 139, 0.08);
    color: #64748b;
    .t-icon { color: #64748b; }
    &:hover { background: rgba(100, 116, 139, 0.12); }
  }

  &.stat-kb {
    background: rgba(7, 192, 95, 0.08);
    color: #07c05f;
    .t-icon { color: #07c05f; }
    &:hover { background: rgba(7, 192, 95, 0.12); }
  }

  &.stat-agent {
    background: rgba(124, 77, 255, 0.08);
    color: #7c4dff;
    .stat-agent-icon {
      width: 14px;
      height: 14px;
      flex-shrink: 0;
      /* 将绿色 icon 着色为紫色，与标签统一 */
      filter: brightness(0) saturate(100%) invert(48%) sepia(79%) saturate(2476%) hue-rotate(236deg);
    }
    &:hover { background: rgba(124, 77, 255, 0.12); }
  }
}

// 待审核角标：与 feature-badge 同高
.pending-requests-badge {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 6px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;
  background: rgba(250, 173, 20, 0.12);
  color: #d48806;
  white-space: nowrap;
}

// 右下角：创建者/角色 合并标签（带图标）
.bottom-right {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}

.relation-role-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 22px;
  padding: 0 6px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;
  font-family: "PingFang SC", system-ui, sans-serif;
  background: rgba(107, 114, 128, 0.08);
  color: #6b7280;

  .t-icon {
    flex-shrink: 0;
    color: #6b7280;
  }

  &.owner {
    background: rgba(124, 77, 255, 0.1);
    color: #7c4dff;
    .t-icon { color: #7c4dff; }
  }

  &.admin {
    background: rgba(7, 192, 95, 0.12);
    color: #07c05f;
    .t-icon { color: #07c05f; }
  }

  &.editor {
    background: rgba(7, 192, 95, 0.08);
    color: #07c05f;
    .t-icon { color: #07c05f; }
  }

  &.viewer {
    background: rgba(107, 114, 128, 0.08);
    color: #6b7280;
    .t-icon { color: #6b7280; }
  }
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
    margin-bottom: 0;
  }

  .empty-state-actions {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 20px;
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
  border: 1px solid #e2e8f0;
  box-shadow: 0 12px 28px rgba(15, 23, 42, 0.12);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.invite-preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 52px 20px 24px;
  background: #ffffff;
  border-bottom: 1px solid #e2e8f0;
  flex-shrink: 0;
  gap: 12px;
}

.invite-preview-back {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #64748b;
  transition: background 0.2s ease, color 0.2s ease;

  &:hover {
    background: #f1f5f9;
    color: #07c05f;
  }
}

.invite-preview-title {
  margin: 0;
  font-family: "PingFang SC", -apple-system, sans-serif;
  font-size: 18px;
  font-weight: 600;
  color: #0f172a;
  letter-spacing: -0.02em;
  flex: 1;
  min-width: 0;
}

.invite-preview-close {
  position: absolute;
  top: 16px;
  right: 16px;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #64748b;
  transition: background 0.2s ease, color 0.2s ease;
  z-index: 10;

  &:hover {
    background: #f1f5f9;
    color: #0f172a;
  }

  &:active {
    background: #e2e8f0;
  }
}

// 加入弹框 body 外层：高度过渡动画（输入邀请码 ↔ 搜索空间 ↔ 查看详情）
.invite-preview-body-wrap {
  flex: 0 0 auto;
  overflow: hidden;
  height: auto;
  transition:
    min-height 0.35s cubic-bezier(0.4, 0, 0.2, 1),
    max-height 0.35s cubic-bezier(0.4, 0, 0.2, 1);
}

.invite-body-inner {
  display: block;
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
    background: #f1f5f9;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #cbd5e1;
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: #07c05f;
    }
  }
}

.join-modal-tabs {
  display: flex;
  gap: 32px;
  margin-bottom: 24px;
  padding-bottom: 4px;
  border-bottom: 1px solid #e2e8f0;

  .join-tab {
    padding: 10px 0;
    cursor: pointer;
    color: #64748b;
    font-size: 14px;
    font-weight: 500;
    user-select: none;
    position: relative;
    transition: color 0.2s ease;
    font-family: "PingFang SC", -apple-system, sans-serif;

    &:hover {
      color: #0f172a;
    }

    &.active {
      color: #07c05f;
      font-weight: 600;

      &::after {
        content: '';
        position: absolute;
        bottom: -5px;
        left: 0;
        right: 0;
        height: 3px;
        background: linear-gradient(90deg, #07c05f, #05a650);
        border-radius: 2px 2px 0 0;
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
  margin-bottom: 16px;
}

// 搜索空间列表容器（与主列表一致：无外框，卡片间距）
.searchable-list-wrap {
  max-height: 360px;
  min-height: 140px;
  overflow-y: auto;
  margin-bottom: 20px;
  padding: 2px 0;

  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: #f1f5f9;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #cbd5e1;
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: #07c05f;
    }
  }
}

// 空状态（与主列表 empty-state 风格一致）
.searchable-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px 20px;
  min-height: 140px;
  text-align: center;

  .searchable-empty-img {
    width: 80px;
    height: 80px;
    margin-bottom: 16px;
    opacity: 0.7;
  }

  .searchable-empty-txt {
    color: #00000099;
    font-family: "PingFang SC", system-ui, sans-serif;
    font-size: 14px;
    font-weight: 500;
    line-height: 1.5;
    max-width: 280px;
  }
}

// 搜索空间卡片列表（与 org-card 视觉一致）
.searchable-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 0;
}

.searchable-card {
  border: 1px solid #e2e8f0;
  border-radius: 14px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: linear-gradient(145deg, #ffffff 0%, #f8fafc 100%);
  position: relative;
  cursor: pointer;
  transition: border-color 0.25s ease, box-shadow 0.25s ease;
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  min-height: 0;

  &::before {
    content: '';
    position: absolute;
    top: 0;
    right: 0;
    width: 80px;
    height: 56px;
    background: radial-gradient(ellipse 60% 50% at 100% 0%, rgba(7, 192, 95, 0.06) 0%, transparent 70%);
    pointer-events: none;
    z-index: 0;
  }

  &:hover:not(.is-full) {
    border-color: rgba(7, 192, 95, 0.5);
    box-shadow: 0 4px 16px rgba(7, 192, 95, 0.08);
  }

  &.is-full {
    cursor: default;
    opacity: 0.88;

    .searchable-card-title {
      color: #64748b;
    }
  }

  .searchable-card-decoration {
    position: absolute;
    top: 6px;
    right: 12px;
    color: rgba(7, 192, 95, 0.35);
    pointer-events: none;
    z-index: 0;
  }

  &:hover:not(.is-full) .searchable-card-decoration {
    color: rgba(7, 192, 95, 0.55);
  }

  .searchable-card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 10px;
    margin-bottom: 8px;
    position: relative;
    z-index: 2;
  }

  .searchable-card-header-left {
    display: flex;
    align-items: center;
    gap: 10px;
    flex: 1;
    min-width: 0;
  }

  .searchable-card-avatar {
    flex-shrink: 0;
  }

  .searchable-card-title {
    color: #1d2129;
    font-family: "PingFang SC", -apple-system, sans-serif;
    font-size: 15px;
    font-weight: 600;
    line-height: 22px;
    letter-spacing: 0.01em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .searchable-card-action {
    flex-shrink: 0;

    .t-button {
      font-size: 12px;
    }
  }

  .searchable-card-content {
    position: relative;
    z-index: 1;
    flex: 1;
    min-height: 0;
    margin-bottom: 10px;
  }

  .searchable-card-desc {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
    margin: 0;
    color: #8c8c8c;
    font-family: "PingFang SC", system-ui, sans-serif;
    font-size: 12px;
    font-weight: 400;
    line-height: 1.5;
  }

  .searchable-card-bottom {
    position: relative;
    z-index: 1;
    padding-top: 10px;
    border-top: 1px solid rgba(226, 232, 240, 0.8);
  }

  .searchable-card-badges {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }

  .searchable-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 4px;
    height: 22px;
    padding: 0 8px;
    border-radius: 6px;
    font-size: 12px;
    font-weight: 500;
    font-family: "PingFang SC", system-ui, sans-serif;

    &.member {
      background: rgba(7, 192, 95, 0.08);
      color: #07c05f;
    }

    &.share {
      background: rgba(0, 82, 217, 0.08);
      color: #0052d9;
    }

    &.searchable-badge-agent {
      background: rgba(7, 192, 95, 0.08);
      color: #07c05f;
      .searchable-badge-agent-icon {
        width: 12px;
        height: 12px;
      }
    }
  }

  .searchable-tag-approval {
    background: rgba(217, 119, 6, 0.1);
    color: #d97706;
    border: none;
  }

  .searchable-tag-full {
    background: rgba(100, 116, 139, 0.1);
    color: #64748b;
    border: none;
  }
}

.invite-preview-input {
  .invite-preview-input-desc {
    font-size: 14px;
    color: #475569;
    margin: 0 0 16px;
    line-height: 1.55;
    font-family: "PingFang SC", -apple-system, sans-serif;
  }
  .invite-preview-input-wrap {
    margin-bottom: 12px;
  }
  .invite-preview-input-tip {
    font-size: 12px;
    color: #64748b;
    margin: 0 0 20px;
    line-height: 1.5;
    font-family: "PingFang SC", -apple-system, sans-serif;
  }
  .invite-preview-error-inline {
    display: flex;
    align-items: center;
    gap: 8px;
    color: #dc2626;
    font-size: 13px;
    margin-bottom: 16px;
    font-family: "PingFang SC", -apple-system, sans-serif;
  }
  .invite-preview-footer-single {
    margin: 24px 0 0;
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
  padding: 64px 28px;
  gap: 20px;

  .invite-preview-loading-text {
    font-size: 14px;
    color: #64748b;
    font-family: "PingFang SC", -apple-system, sans-serif;
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

// 预览内容区域 - 与 org-card / searchable-card 风格一致
.invite-preview-body-preview {
  padding: 24px 24px 0;
}

// 空间详情卡片（与主列表卡片一致）
.preview-detail-card {
  border: 1px solid #e2e8f0;
  border-radius: 14px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: linear-gradient(145deg, #ffffff 0%, #f8fafc 100%);
  position: relative;
  padding: 18px 20px;
  margin-bottom: 20px;

  &::before {
    content: '';
    position: absolute;
    top: 0;
    right: 0;
    width: 120px;
    height: 80px;
    background: radial-gradient(ellipse 60% 50% at 100% 0%, rgba(7, 192, 95, 0.06) 0%, transparent 70%);
    pointer-events: none;
    z-index: 0;
  }
}

.preview-detail-decoration {
  position: absolute;
  top: 8px;
  right: 16px;
  color: rgba(7, 192, 95, 0.35);
  pointer-events: none;
  z-index: 0;
}

.preview-detail-deco-svg {
  display: block;
}

.preview-detail-header {
  position: relative;
  z-index: 2;
  margin-bottom: 12px;
}

.preview-detail-header-left {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.preview-detail-avatar {
  flex-shrink: 0;
}

.preview-detail-title-block {
  flex: 1;
  min-width: 0;
}

.preview-detail-name {
  font-size: 18px;
  font-weight: 600;
  color: #0f172a;
  margin: 0 0 4px;
  font-family: "PingFang SC", system-ui, sans-serif;
  line-height: 1.3;
  letter-spacing: -0.02em;
}

.preview-detail-id-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 12px;
  color: #94a3b8;
  font-family: "PingFang SC", system-ui, sans-serif;
}

.preview-detail-id-label {
  flex-shrink: 0;
}

.preview-detail-id-value {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  font-size: 11px;
  letter-spacing: 0.02em;
  color: #64748b;
}

.preview-detail-id-copy {
  padding: 2px;
  color: #94a3b8;
}
.preview-detail-id-copy:hover {
  color: #0f172a;
}

.preview-detail-content {
  position: relative;
  z-index: 1;
  margin-bottom: 14px;
}

.preview-detail-desc {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  overflow: hidden;
  margin: 0;
  color: #8c8c8c;
  font-family: "PingFang SC", system-ui, sans-serif;
  font-size: 13px;
  font-weight: 400;
  line-height: 1.5;
}

.preview-detail-bottom {
  position: relative;
  z-index: 1;
  padding-top: 12px;
  border-top: 1px solid rgba(226, 232, 240, 0.8);
}

.preview-detail-badges {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.preview-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 26px;
  padding: 0 8px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;
  font-family: "PingFang SC", system-ui, sans-serif;

  &.member {
    background: rgba(7, 192, 95, 0.08);
    color: #07c05f;
  }

  &.share {
    background: rgba(0, 82, 217, 0.08);
    color: #0052d9;
  }

  &.preview-badge-agent {
    background: rgba(7, 192, 95, 0.08);
    color: #07c05f;
    .preview-badge-agent-icon {
      width: 14px;
      height: 14px;
    }
  }
}

.preview-tag-approval {
  background: rgba(217, 119, 6, 0.1);
  color: #d97706;
  border: none;
}

// 加入方式与说明面板
.preview-join-section,
.preview-status-section {
  margin-top: 0;
  padding-bottom: 24px;
}

.preview-join-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  font-size: 14px;
  font-family: "PingFang SC", system-ui, sans-serif;
}

.preview-join-label {
  color: #64748b;
  flex-shrink: 0;
}

.preview-join-value {
  font-weight: 500;

  &.value-success {
    color: #07c05f;
  }

  &.value-warning {
    color: #b45309;
  }
}

.preview-join-note {
  padding: 10px 12px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  font-size: 13px;
  color: #64748b;
  line-height: 1.5;
  font-family: "PingFang SC", system-ui, sans-serif;
  margin-bottom: 16px;

  &.preview-join-note-warning {
    background: #fffbeb;
    border-color: #fde68a;
    color: #b45309;
  }

  &.preview-join-note-success {
    display: flex;
    align-items: center;
    gap: 8px;
    background: #f0fdf4;
    border-color: #bbf7d0;
    color: #07c05f;

    .t-icon {
      flex-shrink: 0;
    }
  }
}

.preview-form-group {
  margin-bottom: 20px;

  &:last-child {
    margin-bottom: 0;
  }
}

.preview-form-label {
  display: block;
  margin-bottom: 8px;
  font-family: "PingFang SC", system-ui, sans-serif;
  font-size: 14px;
  font-weight: 500;
  color: #64748b;
}

.preview-role-select {
  width: 100%;
  max-width: 180px;
}

.preview-message-input {
  width: 100%;
}

.invite-preview-footer {
  padding: 20px 24px;
  border-top: 1px solid #e2e8f0;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.35s cubic-bezier(0.4, 0, 0.2, 1);

  .invite-preview-modal {
    transition: transform 0.35s cubic-bezier(0.34, 1.56, 0.64, 1);
  }
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .invite-preview-modal {
    transform: scale(0.92) translateY(-8px);
  }
}
.modal-enter-to,
.modal-leave-from {
  .invite-preview-modal {
    transform: scale(1) translateY(0);
  }
}
</style>
