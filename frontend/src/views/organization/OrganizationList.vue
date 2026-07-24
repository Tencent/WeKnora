<template>
  <div class="org-list-container">
    <ListSpaceSidebar mode="organization" v-model="spaceSelection" :count-all="organizations.length"
      :count-created="createdCount" :count-joined="joinedCount" />
    <div class="org-list-content">
      <div class="header" style="--wails-draggable: drag">
        <div class="header-title" style="--wails-draggable: drag">
          <div class="title-row" style="--wails-draggable: drag">
            <h2 style="--wails-draggable: drag">{{ $t('organization.title') }}</h2>
            <div class="header-actions" style="--wails-draggable: no-drag">
              <t-tooltip :content="canManageOrg ? $t('organization.joinOrg') : noPermissionTip" placement="bottom">
                <t-button variant="text" theme="default" size="small" class="header-action-btn"
                  style="--wails-draggable: no-drag" :disabled="!canManageOrg" @click="handleJoinOrganization">
                  <template #icon><t-icon name="enter" size="16px" /></template>
                </t-button>
              </t-tooltip>
              <t-tooltip :content="canManageOrg ? $t('organization.createOrg') : noPermissionTip" placement="bottom">
                <t-button variant="text" theme="default" size="small" class="header-action-btn"
                  style="--wails-draggable: no-drag" :disabled="!canManageOrg" @click="handleCreateOrganization">
                  <template #icon><img src="@/assets/img/organization-green.svg" class="org-create-icon" alt=""
                      aria-hidden="true" /></template>
                </t-button>
              </t-tooltip>
            </div>
          </div>
          <p class="header-subtitle" style="--wails-draggable: drag">{{ $t('organization.subtitle') }}</p>
        </div>
      </div>
      <div class="org-list-main">
        <!-- 骨架屏占位 -->
        <div v-if="loading && filteredOrganizations.length === 0" class="org-card-wrap">
          <div v-for="n in 4" :key="'skel-' + n" class="org-card org-card-skeleton">
            <div class="card-header">
              <t-skeleton animation="gradient"
                :row-col="[[{ width: '36px', height: '36px', type: 'circle' }, { width: '50%', height: '20px' }]]" />
            </div>
            <div style="flex:1;margin-top:12px">
              <t-skeleton animation="gradient"
                :row-col="[{ width: '100%', height: '14px' }, { width: '70%', height: '14px' }]" />
            </div>
            <div style="margin-top:auto">
              <t-skeleton animation="gradient"
                :row-col="[[{ width: '60px', height: '22px', type: 'rect' }, { width: '60px', height: '22px', type: 'rect' }]]" />
            </div>
          </div>
        </div>

        <!-- 卡片网格 -->
        <div v-if="filteredOrganizations.length > 0" class="org-card-wrap">
          <template v-for="(org, index) in filteredOrganizations" :key="org.id">
            <!-- 我创建的：仅在 all 视图下出现；created/joined 子视图自身已经
                 隐含了语义，再加标题反而冗余。-->
            <div v-if="spaceSelection === 'all' && org.is_owner && index === 0" class="org-section-header"
              role="button" tabindex="0" @click="toggleOrgSection('created')"
              @keydown.enter.prevent="toggleOrgSection('created')"
              @keydown.space.prevent="toggleOrgSection('created')">
              <t-icon name="user" size="14px" />
              <span>{{ $t('organization.createdByMe') }}</span>
              <span class="org-section-count">{{ orgSectionCounts.created }}</span>
              <t-icon class="org-section-toggle"
                :name="isOrgSectionCollapsed('created') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <!-- 我加入的：第一张非 owner 卡片前打标题（all 视图下） -->
            <div v-if="spaceSelection === 'all' && !org.is_owner
              && (index === 0 || filteredOrganizations[index - 1].is_owner)" class="org-section-header" role="button"
              tabindex="0" @click="toggleOrgSection('joined')"
              @keydown.enter.prevent="toggleOrgSection('joined')"
              @keydown.space.prevent="toggleOrgSection('joined')">
              <t-icon name="usergroup" size="14px" />
              <span>{{ $t('organization.joinedByMe') }}</span>
              <span class="org-section-count">{{ orgSectionCounts.joined }}</span>
              <t-icon class="org-section-toggle"
                :name="isOrgSectionCollapsed('joined') ? 'chevron-right' : 'chevron-down'" size="14px" />
            </div>
            <div v-show="!isOrgRowHidden(org)" class="org-card"
            :class="{ 'joined-org': !org.is_owner }" @click="handleCardClick(org)">
            <!-- 装饰：协作网络感图形 -->
            <div class="card-decoration">
              <svg class="card-deco-svg" width="56" height="40" viewBox="0 0 56 40" fill="none"
                xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
                <circle cx="10" cy="12" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5" />
                <circle cx="28" cy="8" r="5" stroke="currentColor" stroke-width="1.8" fill="none" opacity="0.7" />
                <circle cx="46" cy="14" r="4" stroke="currentColor" stroke-width="1.5" fill="none" opacity="0.5" />
                <path d="M14 13 L24 10 M32 10 L42 13" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"
                  opacity="0.4" />
                <circle cx="28" cy="28" r="6" stroke="currentColor" stroke-width="1.2" fill="none" opacity="0.35" />
                <path d="M28 14 L28 22 M20 18 L26 24 M36 18 L30 24" stroke="currentColor" stroke-width="1"
                  stroke-linecap="round" opacity="0.3" />
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
              <t-popup v-model="organizationMenuVisibility[org.id]" overlayClassName="card-more-popup"
                :on-visible-change="(visible: boolean) => onVisibleChange(visible, org)" trigger="click"
                destroy-on-close placement="bottom-right">
                <div class="more-wrap" @click.stop :class="{ 'active-more': organizationMenuVisibility[org.id] }">
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
                    <div v-if="org.is_owner && canManageOrg" class="popup-menu-item delete"
                      @click.stop="handleDelete(org)">
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
                <t-tooltip v-if="(org.pending_join_request_count ?? 0) > 0"
                  :content="$t('organization.settings.pendingJoinRequestsBadge')" placement="top">
                  <span class="pending-requests-badge">{{ org.pending_join_request_count }} {{
                    $t('organization.settings.pendingReview') }}</span>
                </t-tooltip>
              </div>
              <div v-if="showOrgRelationTag(org)" class="bottom-right">
                <div class="relation-role-tag" :class="org.is_owner ? 'owner' : (org.my_role || '')">
                  <t-icon :name="org.is_owner ? 'usergroup-add' : 'usergroup'" size="14px" />
                  <span>{{ org.is_owner ? $t('organization.owner') : (org.my_role ?
                    $t(`organization.role.${org.my_role}`) :
                    $t('organization.joinedByMe')) }}</span>
                </div>
              </div>
            </div>
          </div>
          </template>
        </div>

        <!-- 空状态（按筛选显示不同文案） -->
        <div v-else-if="!loading" class="empty-state">
          <img class="empty-img" src="@/assets/img/upload.svg" alt="">
          <span class="empty-txt">{{ emptyStateTitle }}</span>
          <span class="empty-desc">{{ emptyStateDesc }}</span>
          <div class="empty-state-actions">
            <t-tooltip :content="noPermissionTip" placement="top" :disabled="canManageOrg">
              <t-button theme="default" variant="outline" class="org-join-btn" :disabled="!canManageOrg"
                @click="handleJoinOrganization">
                <template #icon><t-icon name="enter" /></template>
                {{ $t('organization.joinOrg') }}
              </t-button>
            </t-tooltip>
            <t-tooltip :content="noPermissionTip" placement="top" :disabled="canManageOrg">
              <t-button class="org-create-btn" :disabled="!canManageOrg" @click="handleCreateOrganization">
                <template #icon><img src="@/assets/img/organization-green.svg" class="org-create-icon" alt=""
                    aria-hidden="true" /></template>
                {{ $t('organization.createOrg') }}
              </t-button>
            </t-tooltip>
          </div>
        </div>
      </div>
    </div>

    <!-- Organization Settings Modal (用于创建和编辑组织) -->
    <OrganizationSettingsModal :visible="showSettingsModal" :org-id="settingsOrgId" :mode="settingsMode"
      @update:visible="showSettingsModal = $event" />

    <!-- Delete Confirm Dialog -->
    <t-dialog v-model:visible="deleteVisible" dialogClassName="del-org-dialog" :closeBtn="false" :cancelBtn="null"
      :confirmBtn="null">
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
    <t-dialog v-model:visible="leaveVisible" dialogClassName="del-org-dialog" :closeBtn="false" :cancelBtn="null"
      :confirmBtn="null">
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
          <div class="invite-preview-modal" :class="{
            'is-wide': !invitePreviewData && !invitePreviewLoading && joinStep === 'search'
          }">
            <div class="invite-preview-header">
              <!-- 预览详情且来自搜索时显示返回按钮 -->
              <button v-if="invitePreviewData && !inviteCode" class="invite-preview-back" @click="backFromPreview"
                :aria-label="$t('organization.join.backToSearch')">
                <t-icon name="chevron-left" />
              </button>
              <h2 class="invite-preview-title">{{ invitePreviewData ? $t('organization.invite.previewTitle') :
                $t('organization.joinOrg') }}</h2>
              <button class="invite-preview-close" @click="closeInvitePreview" :aria-label="$t('common.close')">
                <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                  <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
                </svg>
              </button>
            </div>

            <!-- 步骤1/2/Loading 共用高度过渡容器 -->
            <div class="invite-preview-body-wrap" :style="inviteBodyWrapStyle">
              <div ref="inviteBodyInnerRef" class="invite-body-inner">
                <!-- 步骤1：输入邀请码 或 搜索空间 -->
                <div v-if="!invitePreviewLoading && !invitePreviewData"
                  class="invite-preview-body invite-preview-input">
                  <div class="join-mode-pills">
                    <button type="button" :class="['join-mode-pill', { active: joinStep === 'invite' }]"
                      @click="joinStep = 'invite'">
                      {{ $t('organization.join.byInviteCode') }}
                    </button>
                    <button type="button" :class="['join-mode-pill', { active: joinStep === 'search' }]"
                      @click="handleSearchTabClick">
                      {{ $t('organization.join.searchSpaces') }}
                    </button>
                  </div>

                  <!-- Tab 内容容器 - 平滑高度过渡 -->
                  <div ref="tabContentWrapperRef" class="join-tab-content-wrapper">
                    <!-- 输入邀请码 -->
                    <div v-if="joinStep === 'invite'" class="join-tab-content">
                      <template v-if="!invitePreviewError">
                        <div class="join-form-item">
                          <label class="join-form-label">{{ $t('organization.inviteCode') }}</label>
                          <p class="join-form-desc">{{ $t('organization.invite.inputDesc') }}</p>
                          <t-input v-model="joinInputCode" :placeholder="$t('organization.inviteCodePlaceholder')"
                            size="medium" :maxlength="32" clearable @keyup.enter="doPreviewFromInput" />
                          <p class="join-form-tip">{{ $t('organization.editor.inviteCodeTip') }}</p>
                        </div>
                      </template>
                      <template v-else>
                        <div class="invite-preview-error-inline">
                          <t-icon name="error-circle" size="20px" />
                          <span>{{ invitePreviewError }}</span>
                        </div>
                        <div class="join-form-item">
                          <label class="join-form-label">{{ $t('organization.inviteCode') }}</label>
                          <t-input v-model="joinInputCode" :placeholder="$t('organization.inviteCodePlaceholder')"
                            size="medium" :maxlength="32" clearable @keyup.enter="doPreviewFromInput" />
                        </div>
                      </template>
                      <div class="invite-preview-footer invite-preview-footer-single">
                        <t-button theme="default" variant="outline" size="medium" @click="closeInvitePreview">
                          {{ $t('common.cancel') }}
                        </t-button>
                        <t-button theme="primary" size="medium" :loading="invitePreviewLoading"
                          @click="doPreviewFromInput">
                          {{ $t('organization.invite.previewAction') }}
                        </t-button>
                      </div>
                    </div>

                    <!-- 搜索可加入空间 -->
                    <div v-else-if="joinStep === 'search'" class="join-tab-content join-tab-search">
                      <div class="join-form-item join-form-item--compact">
                        <label class="join-form-label">{{ $t('organization.join.searchSpaces') }}</label>
                        <p class="join-form-desc">{{ $t('organization.join.searchSpacesDesc') }}</p>
                        <t-input v-model="searchQuery" :placeholder="$t('organization.join.searchSpacesPlaceholder')"
                          size="medium" clearable @input="doSearchSearchableDebounced" @keyup.enter="doSearchSearchable">
                          <template #prefix-icon>
                            <t-icon name="search" />
                          </template>
                        </t-input>
                      </div>
                      <div class="searchable-list-wrap">
                        <t-loading :loading="searchLoading">
                          <div v-if="searchableList.length === 0 && !searchLoading" class="searchable-empty">
                            <t-empty :description="searchQuery ? $t('organization.join.noSearchResult') :
                              $t('organization.join.noSearchableSpaces')" />
                          </div>
                          <div v-else class="searchable-list">
                            <div v-for="org in searchableList" :key="org.id" class="searchable-row"
                              :class="{ 'is-full': isOrgFull(org) }"
                              @click="!isOrgFull(org) && previewSearchableOrg(org)">
                              <div class="searchable-row-main">
                                <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
                                <div class="searchable-row-info">
                                  <span class="searchable-row-title" :title="org.name">{{ org.name }}</span>
                                  <span class="searchable-row-desc">{{ org.description || $t('organization.noDescription') }}</span>
                                </div>
                              </div>
                              <div class="searchable-row-meta">
                                <span class="searchable-meta-item">
                                  <t-icon name="user" size="12px" />
                                  <template v-if="org.member_limit > 0">{{ org.member_count }}/{{ org.member_limit }}</template>
                                  <template v-else>{{ org.member_count }}</template>
                                </span>
                                <t-tag v-if="org.require_approval" size="small" variant="light" theme="warning">
                                  {{ $t('organization.invite.needApproval') }}
                                </t-tag>
                                <t-tag v-if="isOrgFull(org)" size="small" variant="light">
                                  {{ $t('organization.join.memberLimitReached') }}
                                </t-tag>
                                <t-button v-if="!isOrgFull(org)" theme="primary" variant="outline" size="small"
                                  @click.stop="previewSearchableOrg(org)">
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
                  <t-loading size="medium" />
                  <span class="invite-preview-loading-text">{{ $t('organization.invite.loading') }}</span>
                </div>

                <!-- 步骤2：空间详情预览 -->
                <div v-else-if="invitePreviewData" class="invite-preview-body invite-preview-body-preview">
                  <div class="preview-space-hero">
                    <div class="preview-space-avatar-wrap">
                      <SpaceAvatar :name="invitePreviewData.name" :avatar="invitePreviewData.avatar" size="large" />
                    </div>
                    <h3 class="preview-space-name">{{ invitePreviewData.name }}</h3>
                    <p class="preview-space-desc">{{ invitePreviewData.description || $t('organization.noDescription') }}</p>
                    <div class="feature-badges preview-space-badges">
                      <t-tooltip :content="$t('organization.memberCount')" placement="top">
                        <div class="feature-badge stat-member">
                          <t-icon name="user" size="14px" />
                          <span class="badge-count">{{ invitePreviewData.member_count }}</span>
                        </div>
                      </t-tooltip>
                      <t-tooltip :content="$t('organization.invite.knowledgeBases')" placement="top">
                        <div class="feature-badge stat-kb">
                          <t-icon name="folder" size="14px" />
                          <span class="badge-count">{{ invitePreviewData.share_count }}</span>
                        </div>
                      </t-tooltip>
                      <t-tooltip :content="$t('organization.invite.agents')" placement="top">
                        <div class="feature-badge stat-agent">
                          <img src="@/assets/img/agent-green.svg" class="stat-agent-icon" alt="" aria-hidden="true" />
                          <span class="badge-count">{{ invitePreviewData.agent_share_count ?? 0 }}</span>
                        </div>
                      </t-tooltip>
                    </div>
                    <button type="button" class="preview-space-id-chip" @click="copyPreviewSpaceId">
                      <span class="preview-space-id-label">{{ $t('organization.join.spaceId') }}</span>
                      <code>{{ shortPreviewSpaceId }}</code>
                      <t-icon name="file-copy" size="14px" />
                    </button>
                  </div>

                  <div v-if="invitePreviewData.is_already_member" class="preview-member-status">
                    <t-icon name="check-circle" size="18px" />
                    <span>{{ $t('organization.invite.alreadyMember') }}</span>
                  </div>

                  <div v-else class="preview-join-summary">
                    <div class="preview-info-row">
                      <span class="preview-info-label">{{ $t('organization.invite.approvalLabel') }}</span>
                      <t-tag size="small"
                        :theme="invitePreviewData.require_approval ? 'warning' : 'success'" variant="light">
                        {{ invitePreviewData.require_approval ? $t('organization.invite.needApproval') :
                          $t('organization.invite.noApproval') }}
                      </t-tag>
                    </div>
                    <p v-if="!invitePreviewData.require_approval" class="preview-info-desc">
                      {{ $t('organization.invite.defaultRoleAfterJoin', { role: $t('organization.role.viewer') }) }}
                    </p>
                    <template v-else>
                      <p class="preview-info-desc preview-info-desc--warning">
                        {{ $t('organization.invite.requireApprovalTip') }}
                      </p>
                      <div class="preview-join-fields">
                        <div class="join-form-item join-form-item--compact">
                          <label class="join-form-label">{{ $t('organization.invite.requestRole') }}</label>
                          <t-select v-model="inviteRequestRole" size="medium"
                            :placeholder="$t('organization.invite.selectRole')" :options="orgRoleOptions" />
                        </div>
                        <div class="join-form-item join-form-item--compact">
                          <label class="join-form-label">{{ $t('organization.invite.applicationNote') }}</label>
                          <t-textarea v-model="inviteRequestMessage" size="medium"
                            :placeholder="$t('organization.invite.messagePlaceholder')" :maxlength="500"
                            :autosize="{ minRows: 2, maxRows: 4 }" />
                        </div>
                      </div>
                    </template>
                  </div>

                  <div class="invite-preview-footer">
                    <t-button theme="default" variant="outline" size="medium" @click="backFromPreview">
                      {{ !inviteCode ? $t('organization.join.backToSearch') : $t('common.cancel') }}
                    </t-button>
                    <t-button v-if="!invitePreviewData.is_already_member" theme="primary" size="medium"
                      :loading="inviteJoining" @click="confirmJoinOrganization">
                      {{ invitePreviewData.require_approval ? $t('organization.invite.submitRequest') :
                        $t('organization.invite.primaryJoin') }}
                    </t-button>
                    <t-button v-else theme="primary" size="medium" @click="viewOrganizationFromPreview">
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
import { ref, reactive, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useOrganizationStore } from '@/stores/organization'
import { useAuthStore } from '@/stores/auth'
import type { Organization, OrganizationPreview, SearchableOrganizationItem } from '@/api/organization'
import { previewOrganization, submitJoinRequest } from '@/api/organization'
import { useI18n } from 'vue-i18n'
import OrganizationSettingsModal from './OrganizationSettingsModal.vue'
import SpaceAvatar from '@/components/SpaceAvatar.vue'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'
import { shouldShowOrgRelationTag } from '@/utils/card-list-badge'

type OrgWithUI = Organization

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const orgStore = useOrganizationStore()
const authStore = useAuthStore()

// 后端 /api/v1/organizations 下的写操作（创建、加入、申请加入、邀请、审批、改设置等）
// 在路由层都要求当前空间角色 ≥ admin。前端只用于 UI 渲染，安全边界仍在服务端。
const canManageOrg = computed(
  () => authStore.hasRole('admin') || authStore.canAccessAllTenants
)
const noPermissionTip = computed(() => t('organization.rbac.needTenantAdminTip'))

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
const searchableList = computed(() => orgStore.searchableOrganizations)
const searchLoading = ref(false)
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null
// 搜索结果缓存：避免重复点击时重复请求导致高度跳动
const organizationMenuVisibility = reactive<Record<string, boolean>>({})

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
  if (!canManageOrg.value) {
    MessagePlugin.warning(
      event.detail?.type === 'create'
        ? t('organization.rbac.cannotCreate')
        : t('organization.rbac.cannotJoin')
    )
    return
  }
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
    orgStore.clearSearchableOrganizations()
    // 注意：不清空缓存，保留搜索结果以便下次快速显示
    showInvitePreview.value = true
  }
}) as EventListener

// 左侧筛选：'all' | 'created' | 'joined'
const spaceSelection = ref<'all' | 'created' | 'joined'>('all')

// Computed
const loading = computed(() => orgStore.loading)
const organizations = computed<OrgWithUI[]>(() => orgStore.organizations)

const createdCount = computed(() => organizations.value.filter(o => o.is_owner).length)
const joinedCount = computed(() => organizations.value.filter(o => !o.is_owner).length)

const filteredOrganizations = computed(() => {
  if (spaceSelection.value === 'created') return organizations.value.filter(o => o.is_owner)
  if (spaceSelection.value === 'joined') return organizations.value.filter(o => !o.is_owner)
  // 「全部」视图下把我创建的 owner 排在前面、我加入的排在后面，方便上面的
  // 分组标题在过渡处一次性打出来——和 KB / Agent 列表口径一致。
  return [...organizations.value].sort((a, b) => {
    if (a.is_owner === b.is_owner) return 0
    return a.is_owner ? -1 : 1
  })
})

type OrgSectionKey = 'created' | 'joined'
const collapsedOrgSections = ref<Set<OrgSectionKey>>(new Set())
const isOrgSectionCollapsed = (key: OrgSectionKey) => collapsedOrgSections.value.has(key)
const toggleOrgSection = (key: OrgSectionKey) => {
  const next = new Set(collapsedOrgSections.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  collapsedOrgSections.value = next
}
const orgSectionOf = (org: { is_owner?: boolean }): OrgSectionKey => (org.is_owner ? 'created' : 'joined')
const isOrgRowHidden = (org: { is_owner?: boolean }) =>
  spaceSelection.value === 'all' && isOrgSectionCollapsed(orgSectionOf(org))
const orgSectionCounts = computed<Record<OrgSectionKey, number>>(() => {
  const c: Record<OrgSectionKey, number> = { created: 0, joined: 0 }
  filteredOrganizations.value.forEach(o => { c[orgSectionOf(o)]++ })
  return c
})

function showOrgRelationTag(org: { is_owner?: boolean; my_role?: string }): boolean {
  return shouldShowOrgRelationTag({
    spaceSelection: spaceSelection.value,
    isOwner: !!org.is_owner,
    myRole: org.my_role,
  })
}

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
    organizationMenuVisibility[org.id] = false
  }
}

// 创建组织
function handleCreateOrganization() {
  if (!canManageOrg.value) {
    MessagePlugin.warning(t('organization.rbac.cannotCreate'))
    return
  }
  settingsOrgId.value = ''
  settingsMode.value = 'create'
  showSettingsModal.value = true
}

// 加入组织
function handleJoinOrganization() {
  if (!canManageOrg.value) {
    MessagePlugin.warning(t('organization.rbac.cannotJoin'))
    return
  }
  joinInputCode.value = ''
  inviteCode.value = ''
  invitePreviewData.value = null
  invitePreviewError.value = ''
  invitePreviewLoading.value = false
  joinStep.value = 'invite'
  searchQuery.value = ''
  orgStore.clearSearchableOrganizations()
  showInvitePreview.value = true
}

function handleCardClick(org: OrgWithUI) {
  // 如果弹窗正在显示，不触发设置
  if (organizationMenuVisibility[org.id]) {
    return
  }
  settingsOrgId.value = org.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

function handleSettings(org: OrgWithUI) {
  organizationMenuVisibility[org.id] = false
  settingsOrgId.value = org.id
  settingsMode.value = 'edit'
  showSettingsModal.value = true
}

function handleLeave(org: OrgWithUI) {
  organizationMenuVisibility[org.id] = false
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
  organizationMenuVisibility[org.id] = false
  deletingOrg.value = org
  deleteVisible.value = true
}

async function confirmDelete() {
  if (!deletingOrg.value) return
  if (!canManageOrg.value) {
    MessagePlugin.warning(t('organization.rbac.cannotManage'))
    return
  }
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
  if (!canManageOrg.value) {
    MessagePlugin.warning(t('organization.rbac.cannotJoin'))
    return
  }

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
      const result = await orgStore.join(inviteCode.value)
      if (result) {
        MessagePlugin.success(t('organization.invite.joinSuccess'))
        showInvitePreview.value = false
        inviteCode.value = ''
        invitePreviewData.value = null
        // 清除 URL 中的 invite_code 参数
        router.replace({ path: route.path, query: {} })
      } else {
        MessagePlugin.error(orgStore.error || t('organization.invite.joinFailed'))
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
  orgStore.clearSearchableOrganizations()
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

// 搜索缓存由 Store 统一管理，切换标签时直接读取缓存或请求最新结果
function handleSearchTabClick() {
  joinStep.value = 'search'
  void doSearchSearchable()
}

// 搜索可加入空间
async function doSearchSearchable() {
  const currentQuery = searchQuery.value.trim()

  searchLoading.value = true
  try {
    await orgStore.fetchSearchableOrganizations(currentQuery, { limit: 20 })
  } catch (e) {
    orgStore.clearSearchableOrganizations()
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
    MessagePlugin.error(t('common.copyFailed'))
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
  if (!canManageOrg.value) {
    MessagePlugin.warning(t('organization.rbac.cannotJoin'))
    return
  }

  inviteJoining.value = true
  try {
    // 如果需要审核，传递角色和消息；否则直接加入
    const message = invitePreviewData.value.require_approval ? inviteRequestMessage.value?.trim() || undefined : undefined
    const role = invitePreviewData.value.require_approval ? inviteRequestRole.value : undefined
    const result = await orgStore.joinById(
      invitePreviewData.value.id,
      message,
      role,
      { requiresApproval: invitePreviewData.value.require_approval }
    )
    if (result.success) {
      if (invitePreviewData.value.require_approval) {
        MessagePlugin.success(t('organization.invite.requestSubmitted'))
      } else {
        MessagePlugin.success(t('organization.invite.joinSuccess'))
      }
      showInvitePreview.value = false
      invitePreviewData.value = null
      orgStore.clearSearchableOrganizations()
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
  void orgStore.fetchOrganizations()
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
  margin: 0 16px 0 0;
  height: 100%;
  box-sizing: border-box;
  flex: 1;
  display: flex;
  position: relative;
  min-height: 0;
}

.org-list-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 20px 28px 0 28px;
}

.org-list-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 8px 0;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  flex-shrink: 0;

  .header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  h2 {
    margin: 0;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.org-join-btn {
  border-color: rgba(7, 192, 95, 0.5);
  color: var(--td-brand-color);
  font-weight: 500;
  transition: all 0.2s ease;

  .t-icon {
    color: var(--td-brand-color);
  }

  &:hover {
    background: rgba(7, 192, 95, 0.08);
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);

    .t-icon {
      color: var(--td-brand-color);
    }
  }
}

.org-create-btn {
  background: var(--td-brand-color);
  border: none;
  color: var(--td-text-color-anti);
  font-weight: 500;
  box-shadow: 0 2px 8px rgba(7, 192, 95, 0.25);
  transition: all 0.25s ease;

  &:hover {
    background: var(--td-brand-color);
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
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 400;
  line-height: 20px;
}

.header-action-btn {
  padding: 0 !important;
  min-width: 28px !important;
  width: 28px !important;
  height: 28px !important;
  display: inline-flex !important;
  align-items: center !important;
  justify-content: center !important;
  background: var(--td-bg-color-secondarycontainer) !important;
  border: 1px solid var(--td-component-stroke) !important;
  border-radius: 6px !important;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--td-bg-color-container) 72%, transparent);
  transition: background 0.2s, border-color 0.2s, color 0.2s;

  &:hover {
    background: var(--td-bg-color-secondarycontainer) !important;
    border-color: var(--td-component-stroke) !important;
    color: var(--td-text-color-primary);
  }

  :deep(.t-icon),
  :deep(.btn-icon-wrapper),
  :deep(.org-create-icon) {
    color: var(--td-brand-color);
  }

  :deep(.org-create-icon) {
    width: 16px;
    height: 16px;
  }
}

// Tab 切换样式（下划线式，与整体协作感一致）
.org-tabs {
  display: flex;
  align-items: center;
  gap: 28px;
  border-bottom: 1px solid var(--td-component-stroke);
  margin-bottom: 24px;

  .tab-item {
    padding: 12px 0;
    cursor: pointer;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 400;
    user-select: none;
    position: relative;
    transition: color 0.2s ease;

    &:hover {
      color: var(--td-text-color-secondary);
    }

    &.active {
      color: var(--td-brand-color);
      font-weight: 500;

      &::after {
        content: '';
        position: absolute;
        bottom: -1px;
        left: 0;
        right: 0;
        height: 2px;
        background: var(--td-brand-color);
        border-radius: 1px;
      }
    }
  }
}

@keyframes contentFadeIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.org-card-wrap {
  display: grid;
  gap: 12px;
  grid-template-columns: 1fr;
  animation: contentFadeIn 0.32s ease-out;
}

// 共享空间分组标题——与 KB / Agent 列表口径完全一致（图标 + 名称 + 数量 + 折叠 chevron）。
.org-section-header {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  gap: 6px;
  // 整行只用来铺背景；点击靠子元素冒泡，避免点到标题右侧空白误折叠。
  pointer-events: none;

  & > * {
    pointer-events: auto;
  }
  position: sticky;
  top: 0;
  z-index: 5;
  background: var(--td-bg-color-container);
  box-shadow: 0 -8px 0 0 var(--td-bg-color-container),
    0 4px 0 0 var(--td-bg-color-container);
  padding: 6px 4px 6px 0;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 13px;
  font-weight: 600;
  line-height: 20px;
  cursor: pointer;
  user-select: none;
  outline: none;

  &:hover {
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    box-shadow: 0 0 0 2px var(--td-brand-color-focus, rgba(0, 82, 217, 0.2));
  }

  .t-icon {
    color: inherit;
  }

  .org-section-toggle {
    margin-left: 4px;
    opacity: 0.7;
    transition: opacity 0.15s ease;
  }

  .org-section-count {
    margin-left: 2px;
    padding: 0 6px;
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: 11px;
    line-height: 16px;
    font-weight: 500;
  }

  &:hover .org-section-toggle {
    opacity: 1;
  }
}

.org-card-skeleton {
  cursor: default;
  display: flex;
  flex-direction: column;
  height: 136px;
  min-height: 136px;
}

/* 与知识库 / 智能体列表统一：紧凑 + 1px 描边 */
.org-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: border-color 0.25s ease, box-shadow 0.25s ease, transform 0.2s ease;
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  height: 136px;
  min-height: 136px;

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
    margin-bottom: 6px;
  }

  .card-title {
    font-size: 15px;
    line-height: 22px;
  }

  .card-content {
    position: relative;
    z-index: 1;
    margin-bottom: 6px;
  }

  .card-bottom {
    position: relative;
    z-index: 1;
    padding-top: 6px;
  }

  .card-description {
    font-size: 12px;
    line-height: 17px;
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
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
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
    background: var(--td-bg-color-container-hover);
    opacity: 1 !important;
  }

  &.active-more {
    background: var(--td-bg-color-container-hover);
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
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
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
  border-top: .5px solid var(--td-component-stroke);
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
  font-family: var(--app-font-family);
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
    color: var(--td-text-color-secondary);

    .t-icon {
      color: var(--td-text-color-secondary);
    }

    &:hover {
      background: rgba(100, 116, 139, 0.12);
    }
  }

  &.stat-kb {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-brand-color);

    .t-icon {
      color: var(--td-brand-color);
    }

    &:hover {
      background: rgba(7, 192, 95, 0.12);
    }
  }

  &.stat-agent {
    background: rgba(124, 77, 255, 0.08);
    color: var(--td-brand-color);

    .stat-agent-icon {
      width: 14px;
      height: 14px;
      flex-shrink: 0;
      /* 将绿色 icon 着色为紫色，与标签统一 */
      filter: brightness(0) saturate(100%) invert(48%) sepia(79%) saturate(2476%) hue-rotate(236deg);
    }

    &:hover {
      background: rgba(124, 77, 255, 0.12);
    }
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
  color: var(--td-warning-color);
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
  font-family: var(--app-font-family);
  background: rgba(107, 114, 128, 0.08);
  color: var(--td-text-color-secondary);

  .t-icon {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
  }

  &.owner {
    background: rgba(124, 77, 255, 0.1);
    color: var(--td-brand-color);

    .t-icon {
      color: var(--td-brand-color);
    }
  }

  &.admin {
    background: rgba(7, 192, 95, 0.12);
    color: var(--td-brand-color);

    .t-icon {
      color: var(--td-brand-color);
    }
  }

  &.editor {
    background: rgba(7, 192, 95, 0.08);
    color: var(--td-brand-color);

    .t-icon {
      color: var(--td-brand-color);
    }
  }

  &.viewer {
    background: rgba(107, 114, 128, 0.08);
    color: var(--td-text-color-secondary);

    .t-icon {
      color: var(--td-text-color-secondary);
    }
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
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 16px;
    font-weight: 600;
    line-height: 26px;
    margin-bottom: 8px;
  }

  .empty-desc {
    color: var(--td-text-color-disabled);
    font-family: var(--app-font-family);
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

@media (min-width: 1900px) {
  .org-card-wrap {
    grid-template-columns: repeat(5, 1fr);
  }
}

@media (min-width: 2200px) {
  .org-card-wrap {
    grid-template-columns: repeat(6, 1fr);
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
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
  }

  .del-circle-txt {
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
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
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    cursor: pointer;

    &:hover {
      opacity: 0.8;
    }
  }

  .confirm {
    color: var(--td-error-color);
    margin-left: 40px;

    &:hover {
      opacity: 0.8;
    }
  }
}
</style>

<style lang="less">
/* 下拉菜单样式已统一至 @/assets/dropdown-menu.less */

// 创建对话框样式优化
.create-org-dialog,
.join-org-dialog {
  .t-form-item__label {
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .t-input,
  .t-textarea {
    font-family: var(--app-font-family);
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
  background: var(--td-bg-color-container);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  overflow: hidden;
  display: flex;
  flex-direction: column;

  &.is-wide {
    max-width: 560px;
  }
}

.invite-preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 48px 16px 20px;
  background: var(--td-bg-color-container);
  border-bottom: 1px solid var(--td-component-stroke);
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
  color: var(--td-text-color-secondary);
  transition: background 0.2s ease, color 0.2s ease;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }
}

.invite-preview-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
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
  color: var(--td-text-color-secondary);
  transition: background 0.2s ease, color 0.2s ease;
  z-index: 10;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &:active {
    background: var(--td-bg-color-secondarycontainer);
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
  padding: 20px 24px 0;
  min-height: 0;
  max-height: calc(90vh - 120px);

  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: var(--td-bg-color-component-disabled);
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: var(--td-brand-color);
    }
  }
}

.join-mode-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 20px;
}

.join-mode-pill {
  display: inline-flex;
  align-items: center;
  padding: 6px 14px;
  border: none;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  font: inherit;
  font-size: 13px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: color 0.15s ease, background 0.15s ease;

  &:hover,
  &:focus-visible {
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 8%, var(--td-bg-color-secondarycontainer));
    outline: none;
  }

  &.active {
    background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.join-form-item {
  margin-bottom: 20px;

  &--compact {
    margin-bottom: 12px;
  }

  .join-form-label {
    display: block;
    margin-bottom: 4px;
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .join-form-desc {
    margin: 0 0 10px;
    font-size: 13px;
    color: var(--td-text-color-secondary);
    line-height: 1.5;
  }

  .join-form-tip {
    margin: 8px 0 0;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    line-height: 1.45;
  }

  :deep(.t-input),
  :deep(.t-select),
  :deep(.t-textarea) {
    width: 100%;
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
  max-height: 320px;
  min-height: 120px;
  overflow-y: auto;
  margin-bottom: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);

  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: var(--td-bg-color-component-disabled);
    border-radius: 3px;
    transition: background 0.2s;

    &:hover {
      background: var(--td-brand-color);
    }
  }
}

.searchable-empty {
  padding: 24px 16px;
}

.searchable-list {
  display: flex;
  flex-direction: column;
}

.searchable-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--td-component-stroke);
  cursor: pointer;
  transition: background 0.15s ease;

  &:last-child {
    border-bottom: none;
  }

  &:hover:not(.is-full) {
    background: var(--td-bg-color-container-hover);
  }

  &.is-full {
    cursor: default;
    opacity: 0.72;
  }
}

.searchable-row-main {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1;
}

.searchable-row-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.searchable-row-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.searchable-row-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.searchable-row-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.searchable-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.invite-preview-input {
  .invite-preview-error-inline {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;
    padding: 10px 12px;
    border-radius: 8px;
    background: var(--td-error-color-light);
    color: var(--td-error-color);
    font-size: 13px;
  }

  .invite-preview-footer-single {
    margin-top: 4px;
    padding: 16px 0 20px;
    border-top: 1px solid var(--td-component-stroke);
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
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
  }
}

.invite-preview-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 40px 28px;

  .invite-preview-error-icon {
    color: var(--td-error-color);
    margin-bottom: 20px;
  }

  .invite-preview-error-title {
    font-size: 18px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px;
    font-family: var(--app-font-family);
  }

  .invite-preview-error-desc {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0 0 24px;
    line-height: 1.5;
    font-family: var(--app-font-family);
  }
}

.invite-preview-body-preview {
  padding: 8px 24px 0;

  > .invite-preview-footer {
    margin: 16px -24px 0;
  }
}

.preview-space-hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 8px 0 20px;
}

.preview-space-avatar-wrap {
  margin-bottom: 12px;
}

.preview-space-name {
  margin: 0 0 6px;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.preview-space-desc {
  margin: 0 0 14px;
  max-width: 360px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
}

.preview-space-badges {
  justify-content: center;
  margin-bottom: 12px;
}

.preview-space-id-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 4px 10px;
  border: none;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
  font: inherit;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;

  code {
    font-family: var(--app-font-family-mono);
    font-size: 11px;
    color: var(--td-text-color-secondary);
    background: transparent;
    border: none;
    padding: 0;
  }

  .t-icon {
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
  }

  &:hover {
    background: color-mix(in srgb, var(--td-brand-color) 8%, var(--td-bg-color-secondarycontainer));
    color: var(--td-text-color-secondary);

    code,
    .t-icon {
      color: var(--td-brand-color);
    }
  }
}

.preview-member-status {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 12px 0 4px;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-brand-color);
}

.preview-join-summary {
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.preview-info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 28px;
}

.preview-info-label {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.preview-info-desc {
  margin: 8px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);

  &--warning {
    color: var(--td-warning-color-active);
  }
}

.preview-join-fields {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px dashed var(--td-component-stroke);

  .t-select,
  .t-textarea {
    width: 100%;
  }

  .join-form-item--compact:last-child {
    margin-bottom: 0;
  }
}

.invite-preview-footer {
  padding: 12px 24px 20px;
  border-top: 1px solid var(--td-component-stroke);
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  background: var(--td-bg-color-container);
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
