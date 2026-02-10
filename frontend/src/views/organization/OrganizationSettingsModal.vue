<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="handleClose">
        <div class="settings-modal">
          <!-- 关闭按钮 -->
          <button class="close-btn" @click="handleClose" :aria-label="$t('common.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </button>

          <div class="settings-container">
            <!-- 左侧导航 -->
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ modalTitle }}</h2>
              </div>
              <div class="settings-nav">
                <div 
                  v-for="item in navItems" 
                  :key="item.key"
                  :class="['nav-item', { 'active': currentSection === item.key }]"
                  @click="currentSection = item.key"
                >
                  <img v-if="item.key === 'sharedAgents'" :src="currentSection === 'sharedAgents' ? agentIconActiveSrc : agentIconSrc" class="nav-icon nav-icon-img" alt="" aria-hidden="true" />
                  <t-icon v-else :name="item.icon" class="nav-icon" />
                  <span class="nav-label">{{ item.label }}</span>
                  <span
                    v-if="item.badge != null && (item.key === 'sharedKb' || item.key === 'sharedAgents' ? true : item.badge > 0)"
                    :class="['nav-item-badge', { 'nav-item-badge-count': item.key === 'sharedKb' || item.key === 'sharedAgents' }]"
                  >{{ item.badge }}</span>
                </div>
              </div>
            </div>

            <!-- 右侧内容区域 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <!-- 基本信息 -->
                <div v-show="currentSection === 'basic'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.editor.basicTitle') }}</h2>
                    <p class="section-description">{{ $t('organization.editor.basicDesc') }}</p>
                  </div>
                  
                  <div class="settings-group">
                    <!-- 空间名称与头像：一行展示，头像点击弹出 Emoji 选择 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('organization.name') }} <span class="required">*</span></label>
                        <p class="desc">{{ $t('organization.editor.nameTip') }}</p>
                      </div>
                      <div class="setting-control">
                        <div class="name-input-wrapper">
                          <t-popup
                            v-model="avatarPopoverVisible"
                            trigger="click"
                            placement="bottom-left"
                            :disabled="!isAdmin"
                            overlay-class-name="avatar-emoji-popover"
                          >
                            <div class="avatar-trigger-wrap">
                              <SpaceAvatar :name="formData.name || '?'" :avatar="formData.avatar" size="large" />
                              <span v-if="isAdmin" class="avatar-change-hint">{{ $t('organization.avatar') }}</span>
                            </div>
                            <template #content>
                              <div class="avatar-popover-content" @click.stop>
                                <p class="avatar-popover-title">{{ $t('organization.avatarPickerHint') }}</p>
                                <div class="avatar-emoji-grid">
                                  <button
                                    v-for="emoji in avatarEmojiOptions"
                                    :key="emoji"
                                    type="button"
                                    class="avatar-emoji-btn"
                                    :class="{ 'is-selected': formData.avatar === 'emoji:' + emoji }"
                                    @click="selectAvatarEmoji(emoji)"
                                  >
                                    {{ emoji }}
                                  </button>
                                </div>
                                <t-button
                                  v-if="formData.avatar"
                                  variant="text"
                                  size="small"
                                  class="avatar-clear-btn"
                                  @click="clearAvatarEmoji"
                                >
                                  {{ $t('organization.avatarClear') }}
                                </t-button>
                              </div>
                            </template>
                          </t-popup>
                          <t-input 
                            v-model="formData.name" 
                            :placeholder="$t('organization.namePlaceholder')"
                            :disabled="!isAdmin"
                            class="name-input"
                          />
                        </div>
                      </div>
                    </div>

                    <!-- 空间描述 -->
                    <div class="setting-row">
                      <div class="setting-info">
                        <label>{{ $t('organization.description') }}</label>
                        <p class="desc">{{ $t('organization.editor.descriptionTip') }}</p>
                      </div>
                      <div class="setting-control">
                        <t-textarea 
                          v-model="formData.description" 
                          :placeholder="$t('organization.descriptionPlaceholder')"
                          :autosize="{ minRows: 3, maxRows: 6 }"
                          :maxlength="500"
                          :disabled="!isAdmin"
                        />
                      </div>
                    </div>

                    <!-- 邀请成员 (仅管理员可见) -->
                    <div v-if="isAdmin && orgId" class="setting-row setting-row-vertical">
                      <div class="setting-info full-width">
                        <label>{{ $t('organization.settings.inviteMembers') }}</label>
                        <p class="desc">{{ $t('organization.settings.inviteMembersDesc') }}</p>
                      </div>
                      <div class="setting-control full-width">
                        <div class="invite-card">
                          <!-- 邀请码 -->
                          <div class="invite-method">
                            <div class="invite-method-header">
                              <t-icon name="qrcode" class="invite-icon" />
                              <span class="invite-method-title">{{ $t('organization.inviteCode') }}</span>
                            </div>
                            <div class="invite-code-box">
                              <span class="invite-code-value">{{ inviteCode }}</span>
                              <div class="invite-code-actions">
                                <t-tooltip :content="$t('common.copy')">
                                  <t-button variant="text" size="small" @click="copyInviteCode">
                                    <t-icon name="file-copy" />
                                  </t-button>
                                </t-tooltip>
                                <t-tooltip :content="$t('organization.refreshInviteCode')">
                                  <t-button variant="text" size="small" @click="refreshInviteCode" :loading="refreshingCode">
                                    <t-icon name="refresh" />
                                  </t-button>
                                </t-tooltip>
                              </div>
                            </div>
                            <p v-if="inviteCode" class="invite-remaining">{{ remainingValidityText }}</p>
                          </div>
                          
                          <div class="invite-divider"></div>
                          
                          <!-- 邀请链接有效期 -->
                          <div class="invite-method">
                            <div class="invite-method-header">
                              <t-icon name="time" class="invite-icon" />
                              <span class="invite-method-title">{{ $t('organization.settings.inviteLinkValidity') }}</span>
                            </div>
                            <p class="invite-validity-desc">{{ $t('organization.settings.inviteLinkValidityDesc') }}</p>
                            <t-select
                              v-model="formData.invite_code_validity_days"
                              :options="inviteValidityOptions"
                              size="small"
                              class="invite-validity-select"
                              :disabled="!isAdmin"
                              @change="handleValidityChange"
                            />
                          </div>
                          
                          <div class="invite-divider"></div>
                          
                          <!-- 邀请链接 -->
                          <div class="invite-method">
                            <div class="invite-method-header">
                              <t-icon name="link" class="invite-icon" />
                              <span class="invite-method-title">{{ $t('organization.settings.inviteLink') }}</span>
                            </div>
                            <div class="invite-link-box">
                              <span class="invite-link-value">{{ inviteLink }}</span>
                              <t-tooltip :content="$t('common.copy')">
                                <t-button variant="text" size="small" @click="copyInviteLink">
                                  <t-icon name="file-copy" />
                                </t-button>
                              </t-tooltip>
                            </div>
                          </div>
                          
                          <div class="invite-divider"></div>
                          
                          <!-- 需要审核开关 -->
                          <div class="invite-method">
                            <div class="invite-method-header">
                              <t-icon name="check-circle" class="invite-icon" />
                              <span class="invite-method-title">{{ $t('organization.settings.requireApproval') }}</span>
                            </div>
                            <div class="approval-toggle">
                              <t-switch 
                                v-model="formData.require_approval" 
                                @change="handleApprovalToggle"
                              />
                              <span class="approval-desc">{{ $t('organization.settings.requireApprovalDesc') }}</span>
                            </div>
                          </div>
                          
                          <div class="invite-divider"></div>
                          
                          <!-- 开放可被搜索 -->
                          <div class="invite-method">
                            <div class="invite-method-header">
                              <t-icon name="search" class="invite-icon" />
                              <span class="invite-method-title">{{ $t('organization.settings.searchable') }}</span>
                            </div>
                            <div class="approval-toggle">
                              <t-switch 
                                v-model="formData.searchable" 
                                @change="handleSearchableToggle"
                              />
                              <span class="approval-desc">{{ $t('organization.settings.searchableDesc') }}</span>
                            </div>
                          </div>
                          
                          <div class="invite-divider"></div>
                          
                          <!-- 成员人数上限 -->
                          <div class="invite-method">
                            <div class="invite-method-header">
                              <t-icon name="user-add" class="invite-icon" />
                              <span class="invite-method-title">{{ $t('organization.settings.memberLimit') }}</span>
                            </div>
                            <p class="invite-validity-desc">{{ $t('organization.settings.memberLimitDesc') }}</p>
                            <div class="member-limit-input-row">
                              <t-input-number
                                v-model="formData.member_limit"
                                :min="0"
                                :max="10000"
                                :placeholder="$t('organization.settings.memberLimitPlaceholder')"
                                theme="normal"
                                style="width: 140px;"
                              />
                              <span class="member-limit-hint">{{ $t('organization.settings.memberLimitHint', { count: orgInfo?.member_count ?? 0 }) }}</span>
                            </div>
                          </div>
                        </div>
                      </div>
                    </div>


                  </div>
                </div>

                <!-- 成员管理（含角色权限说明） -->
                <div v-show="currentSection === 'members'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.manageMembers') }}</h2>
                    <p class="section-description">{{ $t('organization.settings.membersDesc') }}</p>
                  </div>

                  <!-- 角色权限说明 -->
                  <div class="permissions-compact">
                    <div class="permissions-compact-header">
                      <span class="permissions-compact-title">{{ $t('organization.editor.permissionsTitle') }}</span>
                      <span class="permissions-compact-desc">{{ $t('organization.editor.permissionsDesc') }}</span>
                    </div>
                    <div class="permissions-compact-grid">
                      <div :class="['perm-role-block', 'admin', { 'is-me': orgInfo?.my_role === 'admin' }]">
                        <div class="perm-role-tag">
                          <t-icon name="user-safety" size="12px" />
                          <span>{{ $t('organization.role.admin') }}</span>
                          <span v-if="orgInfo?.my_role === 'admin'" class="me-badge">{{ $t('common.me') }}</span>
                        </div>
                        <div class="perm-items">
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.viewerPerm1') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.editorPerm1') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.useSharedAgentsPerm') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.shareKBPerm') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.adminPerm1') }}</span>
                        </div>
                      </div>
                      <div :class="['perm-role-block', 'editor', { 'is-me': orgInfo?.my_role === 'editor' }]">
                        <div class="perm-role-tag">
                          <t-icon name="edit" size="12px" />
                          <span>{{ $t('organization.role.editor') }}</span>
                          <span v-if="orgInfo?.my_role === 'editor'" class="me-badge">{{ $t('common.me') }}</span>
                        </div>
                        <div class="perm-items">
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.viewerPerm1') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.editorPerm1') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.useSharedAgentsPerm') }}</span>
                          <span class="perm-item no"><t-icon name="close" size="12px" />{{ $t('organization.editor.shareKBPerm') }}</span>
                          <span class="perm-item no"><t-icon name="close" size="12px" />{{ $t('organization.editor.adminPerm1') }}</span>
                        </div>
                      </div>
                      <div :class="['perm-role-block', 'viewer', { 'is-me': orgInfo?.my_role === 'viewer' }]">
                        <div class="perm-role-tag">
                          <t-icon name="browse" size="12px" />
                          <span>{{ $t('organization.role.viewer') }}</span>
                          <span v-if="orgInfo?.my_role === 'viewer'" class="me-badge">{{ $t('common.me') }}</span>
                        </div>
                        <div class="perm-items">
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.viewerPerm1') }}</span>
                          <span class="perm-item no"><t-icon name="close" size="12px" />{{ $t('organization.editor.editorPerm1') }}</span>
                          <span class="perm-item has"><t-icon name="check" size="12px" />{{ $t('organization.editor.useSharedAgentsPerm') }}</span>
                          <span class="perm-item no"><t-icon name="close" size="12px" />{{ $t('organization.editor.shareKBPerm') }}</span>
                          <span class="perm-item no"><t-icon name="close" size="12px" />{{ $t('organization.editor.adminPerm1') }}</span>
                        </div>
                      </div>
                    </div>
                    <!-- 申请权限升级按钮（非管理员可见） -->
                    <div v-if="canRequestUpgrade" class="permissions-upgrade-action">
                      <t-button 
                        variant="outline" 
                        size="small" 
                        @click="showUpgradeDialog = true"
                        :disabled="hasPendingUpgrade"
                      >
                        <template #icon><t-icon name="arrow-up" /></template>
                        {{ hasPendingUpgrade ? $t('organization.upgrade.pending') : $t('organization.upgrade.requestUpgrade') }}
                      </t-button>
                    </div>
                  </div>

                  <div class="settings-group members-group">
                    <div class="members-header">
                      <div class="members-search">
                        <t-input
                          v-model="memberSearchQuery"
                          :placeholder="$t('common.search')"
                          clearable
                        >
                          <template #prefix-icon>
                            <t-icon name="search" />
                          </template>
                        </t-input>
                      </div>
                      <t-button 
                        v-if="isAdmin" 
                        variant="outline" 
                        size="small"
                        @click="showAddMemberDialog = true"
                      >
                        <template #icon><t-icon name="user-add" /></template>
                        {{ $t('organization.addMember.button') }}
                      </t-button>
                    </div>
                    
                    <t-loading :loading="membersLoading">
                      <div class="members-list">
                        <div 
                          v-for="member in filteredMembers" 
                          :key="member.id" 
                          class="member-item"
                          :class="{ 
                            'is-owner': member.user_id === orgInfo?.owner_id,
                            'is-me': member.user_id === authStore.currentUserId
                          }"
                        >
                          <div class="member-avatar" :class="{ 'is-me': member.user_id === authStore.currentUserId }">
                            <img v-if="member.avatar" :src="member.avatar" alt="" />
                            <t-icon v-else name="user" size="20px" />
                          </div>
                          <div class="member-info">
                            <span class="member-name">
                              {{ member.username }}
                              <span v-if="member.user_id === authStore.currentUserId" class="me-tag">{{ $t('common.me') }}</span>
                            </span>
                            <span class="member-email">{{ member.email }}</span>
                          </div>
                          <div class="member-role">
                            <t-select
                              v-if="isAdmin && member.user_id !== orgInfo?.owner_id"
                              v-model="member.role"
                              :options="roleOptions"
                              size="small"
                              @change="(val: string) => handleRoleChange(member, val)"
                            />
                            <t-tag v-else size="small" :theme="getRoleTheme(member.role)">
                              {{ $t(`organization.role.${member.role}`) }}
                              <span v-if="member.user_id === orgInfo?.owner_id">({{ $t('organization.owner') }})</span>
                            </t-tag>
                          </div>
                          <div v-if="isAdmin && member.user_id !== orgInfo?.owner_id" class="member-actions">
                            <t-button
                              variant="text"
                              theme="danger"
                              size="small"
                              @click="handleRemoveMember(member)"
                            >
                              <t-icon name="delete" />
                            </t-button>
                          </div>
                        </div>
                        <div v-if="filteredMembers.length === 0" class="empty-members">
                          {{ $t('organization.noMembers') }}
                        </div>
                      </div>
                    </t-loading>
                  </div>
                </div>

                <!-- 加入申请（待审核） -->
                <div v-show="currentSection === 'joinRequests'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.settings.joinRequests') }}</h2>
                    <p class="section-description">{{ $t('organization.settings.joinRequestsDesc') }}</p>
                  </div>

                  <div class="settings-group">
                    <t-loading :loading="joinRequestsLoading">
                      <div v-if="joinRequests.length === 0 && !joinRequestsLoading" class="empty-join-requests">
                        <div class="empty-icon">
                          <t-icon name="check-circle" size="48px" />
                        </div>
                        <p class="empty-text">{{ $t('organization.settings.noPendingRequests') }}</p>
                      </div>
                      <div v-else class="join-requests-list">
                        <div
                          v-for="req in joinRequests"
                          :key="req.id"
                          class="join-request-item"
                        >
                          <div class="request-user">
                            <div class="request-avatar">
                              <t-icon name="user" size="20px" />
                            </div>
                            <div class="request-info">
                              <span class="request-name">
                                {{ req.username || req.email || req.user_id }}
                                <t-tag 
                                  v-if="req.request_type === 'upgrade'" 
                                  size="small" 
                                  theme="warning" 
                                  class="request-type-tag"
                                >
                                  {{ $t('organization.upgrade.upgradeRequest') }}
                                </t-tag>
                              </span>
                              <span class="request-email">{{ req.email }}</span>
                              <p v-if="req.message" class="request-message">{{ req.message }}</p>
                              <span v-if="req.request_type === 'upgrade' && req.prev_role" class="request-prev-role">
                                {{ $t('organization.upgrade.currentRole') }}：{{ roleLabel(req.prev_role) }} → {{ roleLabel(req.requested_role) }}
                              </span>
                              <span v-else class="request-requested-role">{{ $t('organization.invite.requestRole') }}：{{ roleLabel(req.requested_role) }}</span>
                              <span class="request-time">{{ formatDate(req.created_at) }}</span>
                            </div>
                          </div>
                          <div class="request-actions">
                            <div class="request-assign-role">
                              <span class="request-assign-label">{{ $t('organization.settings.assignRole') }}</span>
                              <t-select
                                v-model="assignRoleMap[req.id]"
                                class="request-role-select"
                                :options="orgRoleOptions"
                                size="small"
                              />
                            </div>
                            <t-button theme="primary" size="small" :loading="reviewingRequestId === req.id" @click="handleApproveRequest(req)">
                              {{ $t('organization.settings.approve') }}
                            </t-button>
                            <t-button theme="default" variant="outline" size="small" :loading="reviewingRequestId === req.id" @click="handleRejectRequest(req)">
                              {{ $t('organization.settings.reject') }}
                            </t-button>
                          </div>
                        </div>
                      </div>
                    </t-loading>
                  </div>
                </div>

                <!-- 共享知识库（独立侧边栏） -->
                <div v-show="currentSection === 'sharedKb'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.share.sharedKnowledgeBase') }}</h2>
                    <p class="section-description">{{ $t('organization.settings.sharedDesc') }}</p>
                    <p class="section-description permission-calc-hint">
                      <t-tooltip :content="$t('organization.settings.permissionCalcTip')" placement="top">
                        <span class="hint-inner">
                          <t-icon name="info-circle" size="14px" />
                          {{ $t('organization.settings.permissionCalcFormula') }}
                        </span>
                      </t-tooltip>
                    </p>
                  </div>
                  <div class="settings-group">
                    <t-loading :loading="sharesLoading">
                      <div v-if="sharedKnowledgeBases.length === 0 && !sharesLoading" class="empty-shared">
                        <div class="empty-icon">
                          <img src="@/assets/img/zhishiku.svg" class="empty-icon-kb" alt="" aria-hidden="true" />
                        </div>
                        <p class="empty-text">{{ $t('organization.settings.noSharedKB') }}</p>
                        <p class="empty-subtext">{{ $t('organization.settings.noSharedKBTip') }}</p>
                      </div>
                      <div v-else class="shared-list">
                        <div
                          v-for="share in sharedKnowledgeBases"
                          :key="share.id"
                          class="shared-item"
                          @click="handleShareClick(share)"
                        >
                          <div class="shared-icon shared-icon-kb">
                            <img src="@/assets/img/zhishiku.svg" class="shared-icon-kb-img" alt="" aria-hidden="true" />
                          </div>
                          <div class="shared-info">
                            <span class="shared-name">{{ share.knowledge_base_name }}</span>
                            <div class="shared-meta">
                              <span v-if="share.shared_by_username" class="shared-by">
                                <t-icon name="user" size="12px" />
                                {{ share.shared_by_username }}
                              </span>
                              <span class="shared-time">
                                <t-icon name="time" size="12px" />
                                {{ formatDate(share.created_at) }}
                              </span>
                            </div>
                          </div>
                          <div class="shared-permissions">
                            <t-tooltip :content="$t('organization.settings.sharePermissionLabel')" placement="top">
                              <t-tag size="small" :theme="getPermissionTheme(share.permission)" variant="outline" class="perm-tag">
                                {{ $t('organization.settings.sharePermissionLabel') }}: {{ (share.permission === 'editor' || share.permission === 'admin') ? $t('organization.share.permissionEditable') : $t('organization.share.permissionReadonly') }}
                              </t-tag>
                            </t-tooltip>
                            <t-tooltip :content="$t('organization.settings.permissionCalcTip')" placement="top">
                              <t-tag size="small" :theme="getPermissionTheme(share.my_permission ?? share.permission)" class="perm-tag">
                                {{ $t('organization.settings.myPermissionLabel') }}: {{ ((share.my_permission ?? share.permission) === 'editor' || (share.my_permission ?? share.permission) === 'admin') ? $t('organization.share.permissionEditable') : $t('organization.share.permissionReadonly') }}
                              </t-tag>
                            </t-tooltip>
                          </div>
                          <t-popconfirm
                            v-if="isAdmin"
                            :content="$t('organization.settings.removeShareConfirm', { name: share.knowledge_base_name || share.knowledge_base_id })"
                            :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                            :cancel-btn="{ content: $t('common.cancel') }"
                            @confirm="handleRemoveShare(share)"
                          >
                            <t-tooltip :content="$t('organization.settings.removeShareFromOrg')" placement="top">
                              <t-button
                                variant="text"
                                size="small"
                                theme="danger"
                                class="shared-remove-btn"
                                @click.stop
                              >
                                <t-icon name="delete" size="16px" />
                              </t-button>
                            </t-tooltip>
                          </t-popconfirm>
                        </div>
                      </div>
                    </t-loading>
                  </div>
                </div>

                <!-- 共享智能体（独立侧边栏） -->
                <div v-show="currentSection === 'sharedAgents'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.settings.sharedAgents') }}</h2>
                    <p class="section-description">{{ $t('organization.settings.sharedAgentsDesc') }}</p>
                    <p class="section-description permission-calc-hint">
                      <t-tooltip :content="$t('organization.settings.sharedAgentsKbHint')" placement="top" :show-delay="300">
                        <span class="hint-inner">
                          <t-icon name="info-circle" size="14px" />
                          {{ $t('organization.settings.sharedAgentsKbHintShort') }}
                        </span>
                      </t-tooltip>
                    </p>
                  </div>
                  <div class="settings-group">
                    <div v-if="sharedAgents.length === 0" class="empty-shared">
                      <div class="empty-icon">
                        <img src="@/assets/img/agent.svg" class="empty-icon-agent" alt="" aria-hidden="true" />
                      </div>
                      <p class="empty-text">{{ $t('organization.settings.noSharedAgents') }}</p>
                      <p class="empty-subtext">{{ $t('organization.settings.noSharedAgentsTip') }}</p>
                    </div>
                    <div v-else class="shared-list">
                      <div
                        v-for="share in sharedAgents"
                        :key="share.id"
                        class="shared-item"
                        @mouseenter="onSharedAgentMouseEnter(share, $event)"
                        @mousemove="onSharedAgentMouseMove($event)"
                        @mouseleave="onSharedAgentMouseLeave"
                      >
                        <div class="shared-icon shared-icon-agent-wrap">
                          <AgentAvatar :name="share.agent_name || share.agent_id" size="small" />
                        </div>
                        <div class="shared-info">
                          <span class="shared-name">{{ share.agent_name || share.agent_id }}</span>
                          <div class="shared-meta">
                            <span v-if="share.shared_by_username" class="shared-by"><t-icon name="user" size="12px" />{{ share.shared_by_username }}</span>
                            <span class="shared-time"><t-icon name="time" size="12px" />{{ formatDate(share.created_at) }}</span>
                          </div>
                        </div>
                        <t-popconfirm v-if="isAdmin" :content="$t('organization.settings.removeAgentShareConfirm', { name: share.agent_name || share.agent_id })" :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }" :cancel-btn="{ content: $t('common.cancel') }" @confirm="handleRemoveAgentShare(share)">
                          <t-button variant="text" size="small" theme="danger" class="shared-remove-btn" @click.stop><t-icon name="delete" size="16px" /></t-button>
                        </t-popconfirm>
                      </div>
                    </div>
                  </div>
                </div>

              </div>

              <!-- 共享智能体 hover 跟随气泡 -->
              <Teleport to="body">
                <Transition name="agent-scope-popover-fade">
                  <div
                    v-if="agentScopePopover"
                    class="agent-scope-popover-follow"
                    :style="agentScopePopoverStyle"
                  >
                    <div class="agent-scope-popover-card">
                      <div class="agent-scope-popover-name">{{ agentScopePopover.share.agent_name || agentScopePopover.share.agent_id }}</div>
                      <div class="agent-scope-popover-meta">
                        <span v-if="agentScopePopover.share.shared_by_username" class="popover-meta-item">
                          <t-icon name="user" size="12px" /> {{ agentScopePopover.share.shared_by_username }}
                        </span>
                        <span class="popover-meta-item">
                          <t-icon name="time" size="12px" /> {{ formatDate(agentScopePopover.share.created_at) }}
                        </span>
                      </div>
                      <div class="agent-scope-popover-permission">
                        <span class="popover-label">{{ $t('organization.settings.sharePermissionLabel') }}</span>
                        <span class="popover-value">{{ $t('organization.share.permissionReadonly') }}</span>
                      </div>
                      <template v-if="getAgentScopeTags(agentScopePopover.share).length">
                        <div class="agent-scope-popover-divider" />
                        <div class="agent-scope-popover-section-title">{{ $t('agent.shareScope.title') }}</div>
                        <div v-for="(tag, idx) in getAgentScopeTags(agentScopePopover.share)" :key="idx" class="agent-scope-popover-row">{{ tag }}</div>
                      </template>
                    </div>
                  </div>
                </Transition>
              </Teleport>

              <!-- 底部操作按钮 -->
              <div class="settings-footer">
                <t-button variant="outline" @click="handleClose">{{ $t('common.cancel') }}</t-button>
                <t-button
                  v-if="isAdmin"
                  theme="primary"
                  :loading="submitting"
                  @click="handleSave"
                >
                  {{ isCreateMode ? $t('common.create') : $t('common.save') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 移除成员确认弹窗 -->
    <t-dialog
      v-model:visible="showRemoveDialog"
      :header="$t('organization.detail.removeMemberTitle')"
      theme="warning"
      :confirm-btn="$t('common.confirm')"
      :cancel-btn="$t('common.cancel')"
      @confirm="confirmRemoveMember"
    >
      <p>{{ $t('organization.detail.removeMemberConfirm', { name: removingMember?.username }) }}</p>
    </t-dialog>

    <!-- 申请权限升级弹窗 -->
    <t-dialog
      v-model:visible="showUpgradeDialog"
      :header="$t('organization.upgrade.dialogTitle')"
      :confirm-btn="{ content: $t('common.confirm'), loading: upgradeSubmitting }"
      :cancel-btn="$t('common.cancel')"
      @confirm="handleSubmitUpgrade"
    >
      <div class="upgrade-dialog-content">
        <p class="upgrade-current-role">
          {{ $t('organization.upgrade.currentRole') }}：
          <t-tag size="small" :theme="getRoleTheme(orgInfo?.my_role || 'viewer')">
            {{ $t(`organization.role.${orgInfo?.my_role || 'viewer'}`) }}
          </t-tag>
        </p>
        <div class="upgrade-form-item">
          <label>{{ $t('organization.upgrade.selectRole') }}</label>
          <t-select v-model="upgradeForm.requested_role" :options="upgradeRoleOptions" />
        </div>
        <div class="upgrade-form-item">
          <label>{{ $t('organization.upgrade.reason') }}</label>
          <t-textarea 
            v-model="upgradeForm.message" 
            :placeholder="$t('organization.upgrade.reasonPlaceholder')"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :maxlength="500"
          />
        </div>
      </div>
    </t-dialog>

    <!-- 添加成员弹窗 -->
    <t-dialog
      v-model:visible="showAddMemberDialog"
      :header="$t('organization.addMember.dialogTitle')"
      :confirm-btn="{ content: $t('organization.addMember.confirmBtn'), loading: addMemberSubmitting, disabled: !selectedUser }"
      :cancel-btn="$t('common.cancel')"
      @confirm="handleAddMember"
      @close="resetAddMemberDialog"
      width="420px"
    >
      <div class="add-member-dialog">
        <p class="add-member-tip">{{ $t('organization.addMember.tip') }}</p>
        
        <div class="add-member-field">
          <label>{{ $t('organization.addMember.searchUser') }}</label>
          <t-select
            v-model="selectedUser"
            :placeholder="$t('organization.addMember.searchPlaceholder')"
            filterable
            :filter="() => true"
            :loading="userSearchLoading"
            @search="handleUserSearch"
            clearable
            :options="userSearchOptions"
          />
          <p class="field-hint">{{ $t('organization.addMember.searchHint') }}</p>
        </div>

        <div class="add-member-field">
          <label>{{ $t('organization.addMember.selectRole') }}</label>
          <t-select v-model="addMemberRole" :options="addMemberRoleOptions" />
        </div>
      </div>
    </t-dialog>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  getOrganization,
  listMembers,
  updateOrganization,
  updateMemberRole,
  removeMember,
  generateInviteCode,
  listOrgShares,
  listOrgAgentShares,
  listJoinRequests,
  reviewJoinRequest,
  removeShare,
  removeAgentShare,
  requestRoleUpgrade,
  searchUsersForInvite,
  inviteMember,
  type Organization,
  type OrganizationMember,
  type KnowledgeBaseShare,
  type AgentShareResponse,
  type JoinRequestResponse
} from '@/api/organization'
import { useOrganizationStore } from '@/stores/organization'
import { useAuthStore } from '@/stores/auth'
import SpaceAvatar from '@/components/SpaceAvatar.vue'
import AgentAvatar from '@/components/AgentAvatar.vue'
import agentIconSrc from '@/assets/img/agent.svg'
import agentIconActiveSrc from '@/assets/img/agent-green.svg'

const router = useRouter()
const authStore = useAuthStore()
const { t } = useI18n()

const orgStore = useOrganizationStore()

interface Props {
  visible: boolean
  orgId?: string
  mode?: 'view' | 'edit' | 'create'
}

const props = withDefaults(defineProps<Props>(), {
  mode: 'view'
})

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'saved'): void
}>()

// State
const currentSection = ref('basic')
const orgInfo = ref<Organization | null>(null)
const members = ref<OrganizationMember[]>([])
const sharedKnowledgeBases = ref<KnowledgeBaseShare[]>([])
const sharedAgents = ref<AgentShareResponse[]>([])
const joinRequests = ref<JoinRequestResponse[]>([])
const joinRequestsLoading = ref(false)
const reviewingRequestId = ref<string | null>(null)
const sharesLoading = ref(false)
const membersLoading = ref(false)
const memberSearchQuery = ref('')
const submitting = ref(false)
const refreshingCode = ref(false)
const inviteCode = ref('')
const inviteCodeExpiresAt = ref<string | null>(null)
const showRemoveDialog = ref(false)
const removingMember = ref<OrganizationMember | null>(null)
const showUpgradeDialog = ref(false)
const upgradeSubmitting = ref(false)
const hasPendingUpgrade = ref(false)
const upgradeForm = ref({
  requested_role: 'editor' as 'admin' | 'editor' | 'viewer',
  message: ''
})

// 添加成员相关状态
const showAddMemberDialog = ref(false)
const addMemberSubmitting = ref(false)
const userSearchLoading = ref(false)
const userSearchResults = ref<{ id: string; username: string; email: string; avatar?: string }[]>([])
const selectedUser = ref<string>('')
const addMemberRole = ref<'admin' | 'editor' | 'viewer'>('viewer')

const formData = ref({
  name: '',
  description: '',
  avatar: '' as string,
  require_approval: false,
  searchable: false,
  invite_code_validity_days: 7 as number,
  member_limit: 50 as number // 0 = unlimited
})

// 共享智能体 hover 跟随气泡
const agentScopePopover = ref<{ share: AgentShareResponse; x: number; y: number } | null>(null)
const agentScopePopoverTimer = ref<ReturnType<typeof setTimeout> | null>(null)
const POPOVER_OFFSET = 14
const POPOVER_DELAY = 200

// 空间头像可选 Emoji（方案三：Emoji 作为头像）
const avatarEmojiOptions = [
  '🚀', '📁', '👥', '🏢', '💡', '📚', '🌟', '🔧', '📌', '🎯',
  '📂', '🔒', '🌐', '⚡', '🎨', '📊', '🤝', '💼', '📧', '🏠',
  '🔑', '📈', '✨', '📋', '🌍', '💬', '🔔', '📦', '🎉', '🌈'
]
const avatarPopoverVisible = ref(false)

function selectAvatarEmoji(emoji: string) {
  formData.value.avatar = 'emoji:' + emoji
  avatarPopoverVisible.value = false
}
function clearAvatarEmoji() {
  formData.value.avatar = ''
  avatarPopoverVisible.value = false
}

// Computed
const isCreateMode = computed(() => props.mode === 'create')
const isEditMode = computed(() => props.mode === 'edit' || props.mode === 'create')
const isAdmin = computed(() => {
  if (isCreateMode.value) return true
  return orgInfo.value?.my_role === 'admin' || orgInfo.value?.is_owner
})

// 是否可以申请权限升级（非管理员成员可申请）
const canRequestUpgrade = computed(() => {
  if (isCreateMode.value || !props.orgId) return false
  const myRole = orgInfo.value?.my_role
  return myRole && myRole !== 'admin'
})

// 可申请的角色选项（比当前角色高的角色）
const upgradeRoleOptions = computed(() => {
  const myRole = orgInfo.value?.my_role || 'viewer'
  const options = []
  if (myRole === 'viewer') {
    options.push({ label: t('organization.role.editor'), value: 'editor' })
    options.push({ label: t('organization.role.admin'), value: 'admin' })
  } else if (myRole === 'editor') {
    options.push({ label: t('organization.role.admin'), value: 'admin' })
  }
  return options
})

// 添加成员时可选的角色
const addMemberRoleOptions = computed(() => [
  { label: t('organization.role.viewer'), value: 'viewer' },
  { label: t('organization.role.editor'), value: 'editor' },
  { label: t('organization.role.admin'), value: 'admin' },
])

// 用户搜索选项
const userSearchOptions = computed(() => 
  userSearchResults.value.map(user => ({
    label: `${user.username}  ·  ${user.email}`,
    value: user.id,
  }))
)

const modalTitle = computed(() => {
  if (isCreateMode.value) return t('organization.createOrg')
  return t('organization.settings.editTitle')
})

const navItems = computed(() => {
  const items: { key: string; icon: string; label: string; badge?: number }[] = [
    { key: 'basic', icon: 'info-circle', label: t('organization.editor.navBasic') },
  ]
  // 只有在编辑已有组织时才显示成员管理、加入申请（仅管理员）、共享知识库
  if (props.orgId && !isCreateMode.value) {
    items.push({ key: 'members', icon: 'user', label: t('organization.manageMembers') })
    if (isAdmin.value) {
      const pendingCount = orgInfo.value?.pending_join_request_count ?? 0
      items.push({
        key: 'joinRequests',
        icon: 'user-add',
        label: t('organization.settings.joinRequests'),
        badge: pendingCount > 0 ? pendingCount : undefined
      })
    }
    items.push({
      key: 'sharedKb',
      icon: 'folder-open',
      label: t('organization.share.sharedKnowledgeBase'),
      badge: sharedKnowledgeBases.value.length
    })
    items.push({
      key: 'sharedAgents',
      icon: 'control-platform',
      label: t('organization.settings.sharedAgents'),
      badge: sharedAgents.value.length
    })
  }
  return items
})

const roleOptions = computed(() => [
  { label: t('organization.role.admin'), value: 'admin' },
  { label: t('organization.role.editor'), value: 'editor' },
  { label: t('organization.role.viewer'), value: 'viewer' }
])

const filteredMembers = computed(() => {
  const query = memberSearchQuery.value.toLowerCase()
  if (!query) return members.value
  return members.value.filter(m => 
    m.username.toLowerCase().includes(query) || 
    m.email.toLowerCase().includes(query)
  )
})

const inviteLink = computed(() => {
  if (!inviteCode.value) return ''
  return `${window.location.origin}/join?code=${inviteCode.value}`
})

const inviteValidityOptions = computed(() => [
  { label: t('organization.settings.validity1Day'), value: 1 },
  { label: t('organization.settings.validity7Days'), value: 7 },
  { label: t('organization.settings.validity30Days'), value: 30 },
  { label: t('organization.settings.validityNever'), value: 0 }
])

const remainingValidityText = computed(() => {
  const at = inviteCodeExpiresAt.value
  if (!at) return t('organization.settings.remainingValidityNever')
  const exp = new Date(at)
  const now = new Date()
  if (exp.getTime() <= now.getTime()) return t('organization.settings.remainingValidityExpired')
  const days = Math.ceil((exp.getTime() - now.getTime()) / (24 * 60 * 60 * 1000))
  return t('organization.settings.remainingValidity', { n: days })
})

// Methods
const handleClose = () => {
  emit('update:visible', false)
}

const fetchOrgDetail = async () => {
  if (!props.orgId) return
  try {
    const res = await getOrganization(props.orgId)
    if (res.success && res.data) {
      orgInfo.value = res.data
      const validity = res.data.invite_code_validity_days
      const memberLimit = res.data.member_limit
      formData.value = {
        name: res.data.name,
        description: res.data.description || '',
        avatar: res.data.avatar || '',
        require_approval: res.data.require_approval || false,
        searchable: res.data.searchable || false,
        invite_code_validity_days: typeof validity === 'number' ? validity : 7,
        member_limit: typeof memberLimit === 'number' && memberLimit >= 0 ? memberLimit : 50
      }
      inviteCode.value = res.data.invite_code || ''
      inviteCodeExpiresAt.value = res.data.invite_code_expires_at ?? null
      // 初始化是否有待处理的升级申请
      hasPendingUpgrade.value = res.data.has_pending_upgrade || false
    }
  } catch (error) {
    console.error('Failed to fetch org:', error)
  }
}

const fetchMembers = async () => {
  if (!props.orgId) return
  membersLoading.value = true
  try {
    const res = await listMembers(props.orgId)
    if (res.success && res.data) {
      members.value = res.data.members || []
    }
  } catch (error) {
    console.error('Failed to fetch members:', error)
  } finally {
    membersLoading.value = false
  }
}

const fetchSharedKBs = async () => {
  if (!props.orgId) return
  sharesLoading.value = true
  try {
    const [kbRes, agentRes] = await Promise.all([
      listOrgShares(props.orgId),
      listOrgAgentShares(props.orgId)
    ])
    if (kbRes.success && kbRes.data) {
      sharedKnowledgeBases.value = kbRes.data.shares || []
    } else {
      sharedKnowledgeBases.value = []
    }
    if (agentRes.success && agentRes.data) {
      sharedAgents.value = agentRes.data.shares || []
    } else {
      sharedAgents.value = []
    }
  } catch (error) {
    console.error('Failed to fetch shared resources:', error)
    sharedKnowledgeBases.value = []
    sharedAgents.value = []
  } finally {
    sharesLoading.value = false
  }
}

const orgRoleOptions = [
  { label: t('organization.role.viewer'), value: 'viewer' },
  { label: t('organization.role.editor'), value: 'editor' },
  { label: t('organization.role.admin'), value: 'admin' },
]
const assignRoleMap = ref<Record<string, 'viewer' | 'editor' | 'admin'>>({})

function roleLabel(role: string) {
  if (role === 'admin') return t('organization.role.admin')
  if (role === 'editor') return t('organization.role.editor')
  return t('organization.role.viewer')
}

const fetchJoinRequests = async () => {
  if (!props.orgId) return
  joinRequestsLoading.value = true
  try {
    const res = await listJoinRequests(props.orgId)
    if (res.success && res.data) {
      joinRequests.value = res.data.requests || []
      assignRoleMap.value = {}
      joinRequests.value.forEach((r) => {
        const rRole = (r.requested_role === 'admin' || r.requested_role === 'editor' || r.requested_role === 'viewer') ? r.requested_role : 'viewer'
        assignRoleMap.value[r.id] = rRole
      })
    } else {
      joinRequests.value = []
    }
  } catch (error) {
    console.error('Failed to fetch join requests:', error)
    joinRequests.value = []
  } finally {
    joinRequestsLoading.value = false
  }
}

const handleApproveRequest = async (req: JoinRequestResponse) => {
  if (!props.orgId) return
  reviewingRequestId.value = req.id
  const assignRole = assignRoleMap.value[req.id] ?? (req.requested_role === 'admin' || req.requested_role === 'editor' ? req.requested_role : 'viewer')
  try {
    const res = await reviewJoinRequest(props.orgId, req.id, { approved: true, role: assignRole })
    if (res.success) {
      MessagePlugin.success(t('organization.settings.approveSuccess'))
      joinRequests.value = joinRequests.value.filter(r => r.id !== req.id)
      await fetchOrgDetail()
    } else {
      MessagePlugin.error(res.message || t('organization.settings.reviewFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.settings.reviewFailed'))
  } finally {
    reviewingRequestId.value = null
  }
}

const handleRejectRequest = async (req: JoinRequestResponse) => {
  if (!props.orgId) return
  reviewingRequestId.value = req.id
  try {
    const res = await reviewJoinRequest(props.orgId, req.id, { approved: false })
    if (res.success) {
      MessagePlugin.success(t('organization.settings.rejectSuccess'))
      joinRequests.value = joinRequests.value.filter(r => r.id !== req.id)
      await fetchOrgDetail()
    } else {
      MessagePlugin.error(res.message || t('organization.settings.reviewFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.settings.reviewFailed'))
  } finally {
    reviewingRequestId.value = null
  }
}

const handleSave = async () => {
  if (!formData.value.name.trim()) {
    MessagePlugin.warning(t('organization.nameRequired'))
    currentSection.value = 'basic'
    return
  }

  submitting.value = true
  try {
    if (isCreateMode.value) {
      // 创建模式
      const result = await orgStore.create(
        formData.value.name.trim(),
        formData.value.description.trim()
      )
      if (result) {
        MessagePlugin.success(t('organization.createSuccess'))
        emit('saved')
        handleClose()
      } else {
        MessagePlugin.error(orgStore.error || t('organization.createFailed'))
      }
    } else {
      // 编辑模式
      if (!props.orgId) return
      const res = await updateOrganization(props.orgId, {
        name: formData.value.name.trim(),
        description: formData.value.description.trim(),
        avatar: formData.value.avatar || undefined,
        require_approval: formData.value.require_approval,
        searchable: formData.value.searchable,
        invite_code_validity_days: formData.value.invite_code_validity_days,
        member_limit: formData.value.member_limit
      })
      if (res.success) {
        MessagePlugin.success(t('common.saveSuccess'))
        emit('saved')
        handleClose()
      } else {
        MessagePlugin.error(res.message || t('common.saveFailed'))
      }
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.saveFailed'))
  } finally {
    submitting.value = false
  }
}

const handleRoleChange = async (member: OrganizationMember, newRole: string) => {
  if (!props.orgId) return
  try {
    const res = await updateMemberRole(props.orgId, member.user_id, { 
      role: newRole as 'admin' | 'editor' | 'viewer' 
    })
    if (res.success) {
      MessagePlugin.success(t('organization.roleUpdated'))
    } else {
      MessagePlugin.error(res.message || t('organization.roleUpdateFailed'))
      fetchMembers()
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.roleUpdateFailed'))
    fetchMembers()
  }
}

const handleRemoveMember = (member: OrganizationMember) => {
  removingMember.value = member
  showRemoveDialog.value = true
}

const confirmRemoveMember = async () => {
  if (!removingMember.value || !props.orgId) return
  
  try {
    const res = await removeMember(props.orgId, removingMember.value.user_id)
    if (res.success) {
      MessagePlugin.success(t('organization.memberRemoved'))
      showRemoveDialog.value = false
      fetchMembers()
    } else {
      MessagePlugin.error(res.message || t('organization.memberRemoveFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.memberRemoveFailed'))
  }
}

const handleSubmitUpgrade = async () => {
  if (!props.orgId) return
  
  upgradeSubmitting.value = true
  try {
    const res = await requestRoleUpgrade(props.orgId, {
      requested_role: upgradeForm.value.requested_role,
      message: upgradeForm.value.message
    })
    if (res.success) {
      MessagePlugin.success(t('organization.upgrade.submitSuccess'))
      showUpgradeDialog.value = false
      hasPendingUpgrade.value = true
      // Reset form
      upgradeForm.value = { requested_role: 'editor', message: '' }
    } else {
      MessagePlugin.error(res.message || t('organization.upgrade.submitFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.upgrade.submitFailed'))
  } finally {
    upgradeSubmitting.value = false
  }
}

// 添加成员：搜索用户
let userSearchTimer: ReturnType<typeof setTimeout> | null = null
const handleUserSearch = (query: string) => {
  if (userSearchTimer) {
    clearTimeout(userSearchTimer)
  }
  if (!query || query.length < 2) {
    userSearchResults.value = []
    return
  }
  userSearchTimer = setTimeout(async () => {
    if (!props.orgId) return
    userSearchLoading.value = true
    try {
      const res = await searchUsersForInvite(props.orgId, query, 10)
      if (res.success && res.data) {
        userSearchResults.value = res.data
      }
    } catch (error) {
      console.error('Failed to search users:', error)
    } finally {
      userSearchLoading.value = false
    }
  }, 300)
}

// 添加成员：提交
const handleAddMember = async () => {
  if (!props.orgId || !selectedUser.value) return
  
  addMemberSubmitting.value = true
  try {
    const res = await inviteMember(props.orgId, {
      user_id: selectedUser.value,
      role: addMemberRole.value
    })
    if (res.success) {
      MessagePlugin.success(t('organization.addMember.success'))
      showAddMemberDialog.value = false
      resetAddMemberDialog()
      fetchMembers() // 刷新成员列表
    } else {
      MessagePlugin.error(res.message || t('organization.addMember.failed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.addMember.failed'))
  } finally {
    addMemberSubmitting.value = false
  }
}

// 重置添加成员弹窗
const resetAddMemberDialog = () => {
  selectedUser.value = ''
  addMemberRole.value = 'viewer'
  userSearchResults.value = []
}

const fallbackCopyText = (text: string) => {
  const textArea = document.createElement('textarea')
  textArea.value = text
  textArea.style.position = 'fixed'
  textArea.style.opacity = '0'
  document.body.appendChild(textArea)
  textArea.select()
  document.execCommand('copy')
  document.body.removeChild(textArea)
}

const copyInviteCode = async () => {
  if (inviteCode.value) {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(inviteCode.value)
      } else {
        fallbackCopyText(inviteCode.value)
      }
      MessagePlugin.success(t('common.copied'))
    } catch {
      fallbackCopyText(inviteCode.value)
      MessagePlugin.success(t('common.copied'))
    }
  }
}

const copyInviteLink = async () => {
  if (inviteLink.value) {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(inviteLink.value)
      } else {
        fallbackCopyText(inviteLink.value)
      }
      MessagePlugin.success(t('common.copied'))
    } catch {
      fallbackCopyText(inviteLink.value)
      MessagePlugin.success(t('common.copied'))
    }
  }
}

const refreshInviteCode = async () => {
  if (!props.orgId) return
  refreshingCode.value = true
  try {
    const res = await generateInviteCode(props.orgId) as any
    if (res.success) {
      inviteCode.value = res.invite_code || (res as any).data?.invite_code
      MessagePlugin.success(t('organization.inviteCodeRefreshed'))
      await fetchOrgDetail()
    } else {
      MessagePlugin.error(res.message || t('organization.inviteCodeRefreshFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.inviteCodeRefreshFailed'))
  } finally {
    refreshingCode.value = false
  }
}

const handleValidityChange = async (value: number) => {
  if (!props.orgId) return
  try {
    const res = await updateOrganization(props.orgId, { invite_code_validity_days: value })
    if (res.success) {
      MessagePlugin.success(t('common.saveSuccess'))
    } else {
      formData.value.invite_code_validity_days = orgInfo.value?.invite_code_validity_days ?? 7
      MessagePlugin.error(res.message || t('common.saveFailed'))
    }
  } catch (error: any) {
    formData.value.invite_code_validity_days = orgInfo.value?.invite_code_validity_days ?? 7
    MessagePlugin.error(error?.message || t('common.saveFailed'))
  }
}

// 切换审核开关时立即保存
const handleApprovalToggle = async (value: boolean) => {
  if (!props.orgId) return
  try {
    const res = await updateOrganization(props.orgId, {
      require_approval: value
    })
    if (res.success) {
      MessagePlugin.success(t('common.saveSuccess'))
    } else {
      // 回滚
      formData.value.require_approval = !value
      MessagePlugin.error(res.message || t('common.saveFailed'))
    }
  } catch (error: any) {
    // 回滚
    formData.value.require_approval = !value
    MessagePlugin.error(error?.message || t('common.saveFailed'))
  }
}

// 切换开放可被搜索时立即保存
const handleSearchableToggle = async (value: boolean) => {
  if (!props.orgId) return
  try {
    const res = await updateOrganization(props.orgId, {
      searchable: value
    })
    if (res.success) {
      MessagePlugin.success(t('common.saveSuccess'))
    } else {
      formData.value.searchable = !value
      MessagePlugin.error(res.message || t('common.saveFailed'))
    }
  } catch (error: any) {
    formData.value.searchable = !value
    MessagePlugin.error(error?.message || t('common.saveFailed'))
  }
}

const handleShareClick = (share: KnowledgeBaseShare) => {
  handleClose()
  router.push(`/platform/knowledge-bases/${share.knowledge_base_id}`)
}

const handleRemoveShare = async (share: KnowledgeBaseShare) => {
  if (!props.orgId) return
  try {
    const res = await removeShare(share.knowledge_base_id, share.id)
    if (res.success) {
      MessagePlugin.success(t('organization.settings.removeShareSuccess'))
      sharedKnowledgeBases.value = sharedKnowledgeBases.value.filter(s => s.id !== share.id)
    } else {
      MessagePlugin.error(res.message || t('organization.settings.removeShareFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.settings.removeShareFailed'))
  }
}

const handleRemoveAgentShare = async (share: AgentShareResponse) => {
  if (!props.orgId) return
  try {
    const res = await removeAgentShare(share.agent_id, share.id)
    if (res.success) {
      MessagePlugin.success(t('organization.settings.removeShareSuccess'))
      sharedAgents.value = sharedAgents.value.filter(s => s.id !== share.id)
    } else {
      MessagePlugin.error(res.message || t('organization.settings.removeShareFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.settings.removeShareFailed'))
  }
}

const formatDate = (dateStr: string) => {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

/** 共享智能体能力范围标签（知识库、网络搜索、MCP） */
function getAgentScopeTags(share: AgentShareResponse): string[] {
  const tags: string[] = []
  if (share.scope_kb !== undefined && share.scope_kb !== '') {
    const kbText = share.scope_kb === 'all'
      ? t('agent.shareScope.kbAll')
      : share.scope_kb === 'selected' && (share.scope_kb_count ?? 0) > 0
        ? t('agent.shareScope.kbSelected', { count: share.scope_kb_count })
        : t('agent.shareScope.kbNone')
    tags.push(`${t('agent.shareScope.knowledgeBase')}：${kbText}`)
  }
  if (share.scope_web_search !== undefined) {
    tags.push(`${t('agent.shareScope.webSearch')}：${share.scope_web_search ? t('agent.shareScope.enabled') : t('agent.shareScope.disabled')}`)
  }
  if (share.scope_mcp !== undefined && share.scope_mcp !== '') {
    const mcpText = share.scope_mcp === 'all'
      ? t('agent.shareScope.mcpAll')
      : share.scope_mcp === 'selected' && (share.scope_mcp_count ?? 0) > 0
        ? t('agent.shareScope.mcpSelected', { count: share.scope_mcp_count })
        : t('agent.shareScope.mcpNone')
    tags.push(`${t('agent.shareScope.mcp')}：${mcpText}`)
  }
  return tags
}

function onSharedAgentMouseEnter(share: AgentShareResponse, e: MouseEvent) {
  agentScopePopoverTimer.value = setTimeout(() => {
    agentScopePopover.value = { share, x: e.clientX, y: e.clientY }
    agentScopePopoverTimer.value = null
  }, POPOVER_DELAY)
}

function onSharedAgentMouseMove(e: MouseEvent) {
  if (agentScopePopover.value) {
    agentScopePopover.value = { ...agentScopePopover.value, x: e.clientX, y: e.clientY }
  }
}

function onSharedAgentMouseLeave() {
  if (agentScopePopoverTimer.value) {
    clearTimeout(agentScopePopoverTimer.value)
    agentScopePopoverTimer.value = null
  }
  agentScopePopover.value = null
}

const agentScopePopoverStyle = computed(() => {
  if (!agentScopePopover.value) return {}
  const { x, y } = agentScopePopover.value
  const popoverWidth = 240
  const popoverHeight = 180
  let left = x + POPOVER_OFFSET
  let top = y + POPOVER_OFFSET
  const rightEdge = window.innerWidth - popoverWidth - 12
  const bottomEdge = window.innerHeight - popoverHeight - 12
  if (left > rightEdge) left = rightEdge
  if (left < 12) left = 12
  if (top > bottomEdge) top = bottomEdge
  if (top < 12) top = 12
  return { left: `${left}px`, top: `${top}px` }
})

const getRoleTheme = (role: string) => {
  switch (role) {
    case 'admin': return 'primary'
    case 'editor': return 'warning'
    case 'viewer': return 'default'
    default: return 'default'
  }
}

const getPermissionTheme = (permission: string) => {
  switch (permission) {
    case 'admin': return 'primary'
    case 'editor': return 'warning'
    case 'viewer': return 'default'
    default: return 'default'
  }
}

// Watch
watch(() => props.visible, (newVal) => {
  if (newVal) {
    currentSection.value = 'basic'
    memberSearchQuery.value = ''
    joinRequests.value = []
    if (props.mode === 'create') {
      // 创建模式：重置表单
      formData.value = { name: '', description: '', avatar: '', require_approval: false, searchable: false, invite_code_validity_days: 7, member_limit: 50 }
      orgInfo.value = null
      members.value = []
      sharedKnowledgeBases.value = []
      inviteCode.value = ''
      inviteCodeExpiresAt.value = null
    } else if (props.orgId) {
      fetchOrgDetail()
      fetchMembers()
      fetchSharedKBs()
    }
  } else {
    if (agentScopePopoverTimer.value) {
      clearTimeout(agentScopePopoverTimer.value)
      agentScopePopoverTimer.value = null
    }
    agentScopePopover.value = null
  }
})

watch(currentSection, (section) => {
  if (section === 'joinRequests' && props.orgId) {
    fetchJoinRequests()
  }
})
</script>

<style scoped lang="less">
@primary-color: #07c05f;
@primary-light: rgba(7, 192, 95, 0.1);
@primary-lighter: #e5e5e5;
@primary-hover: #05a04f;

.settings-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2000;
  backdrop-filter: blur(4px);
}

.settings-modal {
  position: relative;
  width: 90vw;
  max-width: 1100px;
  height: 85vh;
  max-height: 750px;
  background: #fff;
  border-radius: 16px;
  box-shadow:
    0 0 0 1px rgba(0, 0, 0, 0.04),
    0 4px 6px -1px rgba(15, 23, 42, 0.06),
    0 12px 24px -4px rgba(15, 23, 42, 0.1),
    0 24px 48px -8px rgba(15, 23, 42, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.close-btn {
  position: absolute;
  top: 20px;
  right: 20px;
  width: 36px;
  height: 36px;
  border: none;
  background: rgba(0, 0, 0, 0.04);
  border-radius: 10px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #64748b;
  transition: background 0.2s ease, color 0.2s ease, transform 0.15s ease;
  z-index: 10;

  &:hover {
    background: rgba(0, 0, 0, 0.08);
    color: #0f172a;
    transform: scale(1.02);
  }

  &:active {
    transform: scale(0.98);
  }
}

.settings-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.settings-sidebar {
  width: 200px;
  background: linear-gradient(180deg, #f8fafc 0%, #f1f5f9 100%);
  border-right: 1px solid rgba(0, 0, 0, 0.06);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;

  .sidebar-header {
    padding: 26px 20px;
    border-bottom: 1px solid rgba(0, 0, 0, 0.06);

    .sidebar-title {
      margin: 0;
      font-family: "PingFang SC", -apple-system, sans-serif;
      font-size: 18px;
      font-weight: 600;
      color: #0f172a;
      letter-spacing: -0.02em;
    }
  }

  .settings-nav {
    flex: 1;
    padding: 12px 8px;
    overflow-y: auto;

    .nav-item {
      display: flex;
      align-items: center;
      padding: 12px 14px;
      margin-bottom: 4px;
      border-radius: 10px;
      cursor: pointer;
      transition: background 0.2s ease, color 0.2s ease;
      font-family: "PingFang SC", -apple-system, sans-serif;
      font-size: 14px;
      color: #64748b;
      font-weight: 500;

      .nav-icon {
        margin-right: 10px;
        font-size: 18px;
        flex-shrink: 0;
        display: flex;
        align-items: center;
        justify-content: center;
        color: #94a3b8;
        transition: color 0.2s;

        &.nav-icon-img {
          width: 18px;
          height: 18px;
        }
      }

      .nav-label {
        flex: 1;
        min-width: 0;
      }

      .nav-item-badge {
        min-width: 20px;
        height: 20px;
        padding: 0 6px;
        border-radius: 10px;
        background: rgba(250, 173, 20, 0.18);
        color: #b45309;
        font-size: 12px;
        font-weight: 600;
        line-height: 20px;
        text-align: center;
        flex-shrink: 0;

        &.nav-item-badge-count {
          background: rgba(0, 0, 0, 0.06);
          color: #64748b;
          font-weight: 500;
        }
      }

      &:hover {
        background: rgba(0, 0, 0, 0.04);

        .nav-icon {
          color: #64748b;
        }
      }

      &.active {
        background: rgba(7, 192, 95, 0.1);
        color: @primary-color;
        font-weight: 600;

        .nav-icon {
          color: @primary-color;
        }
      }
    }
  }
}

.settings-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding: 24px 32px;
}

.section {
  .section-header {
    margin-bottom: 20px;

    h2 {
      margin: 0 0 8px 0;
      font-family: "PingFang SC";
      font-size: 16px;
      font-weight: 600;
      color: #000000e6;
    }

    .section-description {
      margin: 0;
      font-family: "PingFang SC";
      font-size: 14px;
      color: #00000066;
      line-height: 22px;
    }

    .permission-calc-hint {
      margin-top: 6px;

      .hint-inner {
        display: inline-flex;
        align-items: center;
        gap: 6px;
        cursor: help;
        color: #00000099;
        font-size: 13px;
      }
    }
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 16px 0;
  border-bottom: 1px solid #e5e5e5;

  &:first-child {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
  }

  .setting-info {
    flex: 1;
    max-width: 45%;
    padding-right: 20px;

    &.full-width {
      max-width: 100%;
      padding-right: 0;
    }

    label {
      display: block;
      font-size: 14px;
      font-weight: 600;
      color: #1d2129;
      margin-bottom: 4px;

      .required {
        color: #e34d59;
        margin-left: 2px;
      }
    }

    .desc {
      font-size: 13px;
      color: #86909c;
      margin: 0;
      line-height: 1.5;
    }
  }

  .setting-control {
    flex: 1;
    max-width: 50%;
    min-width: 0;

    &.full-width {
      max-width: 100%;
    }
  }

  &.setting-row-vertical {
    flex-direction: column;
    gap: 12px;
  }
}

.avatar-trigger-wrap {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  flex-shrink: 0;
  padding: 4px;
  border-radius: 12px;
  transition: background 0.2s ease;
}
.avatar-trigger-wrap:hover {
  background: rgba(0, 0, 0, 0.04);
}
.avatar-change-hint {
  font-size: 11px;
  color: #94a3b8;
  line-height: 1.2;
}

.name-input-wrapper {
  display: flex;
  align-items: center;
  gap: 12px;
}
.name-input-wrapper .name-input {
  flex: 1;
  min-width: 0;
}

/* 头像 Emoji 弹层内容 */
.avatar-popover-content {
  padding: 12px;
  min-width: 260px;
}
.avatar-popover-title {
  margin: 0 0 10px 0;
  font-size: 12px;
  color: #64748b;
  line-height: 1.4;
}
.avatar-popover-content .avatar-emoji-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-width: 280px;
}
.avatar-popover-content .avatar-emoji-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  padding: 0;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  font-size: 18px;
  cursor: pointer;
  transition: border-color 0.2s ease, background 0.2s ease;
}
.avatar-popover-content .avatar-emoji-btn:hover {
  border-color: #07c05f;
  background: rgba(7, 192, 95, 0.06);
}
.avatar-popover-content .avatar-emoji-btn.is-selected {
  border-color: #07c05f;
  background: rgba(7, 192, 95, 0.12);
}
.avatar-popover-content .avatar-clear-btn {
  margin-top: 10px;
  color: #64748b;
  font-size: 12px;
}
.avatar-popover-content .avatar-clear-btn:hover {
  color: #059669;
}

// 邀请卡片样式
.invite-card {
  background: #f5f5f5;
  border: 1px solid #e5e5e5;
  border-radius: 10px;
  padding: 16px;

  .invite-method {
    .invite-method-header {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-bottom: 10px;

      .invite-icon {
        font-size: 16px;
        color: @primary-color;
      }

      .invite-method-title {
        font-size: 13px;
        font-weight: 600;
        color: #000000e6;
      }
    }
  }

  .invite-code-box {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: #fff;
    border: 1px solid #e5e5e5;
    border-radius: 8px;
    padding: 10px 14px;

    .invite-code-value {
      font-family: 'SF Mono', Monaco, 'Courier New', monospace;
      font-size: 16px;
      font-weight: 600;
      letter-spacing: 2px;
      color: @primary-color;
    }

    .invite-code-actions {
      display: flex;
      gap: 4px;
    }
  }

  .invite-remaining {
    margin: 8px 0 0;
    font-size: 12px;
    color: #86909c;
  }

  .invite-validity-desc {
    font-size: 12px;
    color: #86909c;
    margin: 4px 0 10px;
    line-height: 1.4;
  }

  .invite-validity-select {
    min-width: 140px;
  }

  .member-limit-input-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 8px;

    .member-limit-hint {
      font-size: 12px;
      color: #86909c;
    }
  }

  .invite-divider {
    height: 1px;
    background: #e5e5e5;
    margin: 12px 0;
  }

  .invite-link-box {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: #fff;
    border: 1px solid #e5e5e5;
    border-radius: 8px;
    padding: 10px 14px;
    gap: 12px;

    .invite-link-value {
      flex: 1;
      font-size: 12px;
      color: #4e5969;
      word-break: break-all;
      line-height: 1.4;
    }
  }

  .approval-toggle {
    display: flex;
    align-items: center;
    gap: 12px;

    .approval-desc {
      font-size: 13px;
      color: #8c8c8c;
    }
  }
}

// 成员权限紧凑展示
.permissions-compact {
  margin-bottom: 20px;
  padding: 12px;
  background: #f7f8fa;
  border-radius: 8px;

  .permissions-compact-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 10px;

    .permissions-compact-title {
      font-size: 13px;
      font-weight: 600;
      color: #1d2129;
    }

    .permissions-compact-desc {
      font-size: 12px;
      color: #86909c;
    }
  }

  .permissions-compact-grid {
    display: flex;
    gap: 8px;
  }

  .permissions-upgrade-action {
    margin-top: 10px;
    padding-top: 10px;
    border-top: 1px dashed #e5e6eb;
    display: flex;
    justify-content: flex-end;

    .t-button {
      display: inline-flex;
      align-items: center;
      gap: 4px;

      .t-icon {
        font-size: 14px;
      }
    }
  }

  .perm-role-block {
    flex: 1;
    background: #fff;
    border-radius: 6px;
    padding: 10px;
    border: 1px solid #e5e6eb;
    transition: all 0.15s ease;
    position: relative;

    &.is-me {
      border-left: 3px solid @primary-color;
      background: rgba(7, 192, 95, 0.04);
    }

    .perm-role-tag {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      padding: 2px 8px;
      border-radius: 4px;
      font-size: 12px;
      font-weight: 500;
      margin-bottom: 8px;

      .me-badge {
        padding: 0 4px;
        background: @primary-color;
        color: #fff;
        border-radius: 3px;
        font-size: 10px;
        font-weight: 500;
        margin-left: 2px;
      }
    }

    &.admin .perm-role-tag {
      background: rgba(7, 192, 95, 0.1);
      color: @primary-color;
    }

    &.editor .perm-role-tag {
      background: rgba(237, 112, 46, 0.1);
      color: #ed702e;
    }

    &.viewer .perm-role-tag {
      background: rgba(134, 144, 156, 0.1);
      color: #86909c;
    }

    .perm-items {
      display: flex;
      flex-direction: column;
      gap: 4px;
    }

    .perm-item {
      display: flex;
      align-items: center;
      gap: 4px;
      font-size: 11px;
      line-height: 1.3;

      &.has {
        color: #4e5969;

        .t-icon {
          color: @primary-color;
        }
      }

      &.no {
        color: #c9ced6;
        text-decoration: line-through;

        .t-icon {
          color: #c9ced6;
        }
      }
    }
  }
}

// Members
.members-header {
  display: flex;
  gap: 12px;
  margin-bottom: 16px;
  align-items: center;

  .members-search {
    flex: 1;
  }
}

.members-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 400px;
  overflow-y: auto;

  .member-item {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    background: #f7f9fc;
    border-radius: 8px;
    transition: background 0.2s;

    &:hover {
      background: #f0f2f5;
    }

    &.is-me {
      border: 1px solid @primary-color;
      background: rgba(7, 192, 95, 0.04);
    }

    .member-avatar {
      width: 36px;
      height: 36px;
      border-radius: 50%;
      background: #e5e9f2;
      display: flex;
      align-items: center;
      justify-content: center;
      overflow: hidden;
      color: #86909c;

      &.is-me {
        background: rgba(7, 192, 95, 0.15);
        color: @primary-color;
        box-shadow: 0 0 0 2px @primary-color;
      }

      img {
        width: 100%;
        height: 100%;
        object-fit: cover;
      }
    }

    .member-info {
      flex: 1;
      min-width: 0;

      .member-name {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 14px;
        font-weight: 500;
        color: #1d2129;

        .me-tag {
          display: inline-flex;
          align-items: center;
          padding: 0 5px;
          height: 16px;
          background: @primary-color;
          color: #fff;
          border-radius: 3px;
          font-size: 10px;
          font-weight: 500;
          flex-shrink: 0;
        }
      }

      .member-email {
        display: block;
        font-size: 12px;
        color: #86909c;
      }
    }

    .member-role {
      flex-shrink: 0;
    }

    .member-actions {
      flex-shrink: 0;
    }
  }

  .empty-members {
    padding: 32px;
    text-align: center;
    color: #86909c;
    font-size: 14px;
  }
}

// Shared KBs
.empty-shared {
  padding: 48px 24px;
  text-align: center;

  .empty-icon {
    width: 80px;
    height: 80px;
    margin: 0 auto 16px;
    border-radius: 50%;
    background: #f7f9fc;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #c9ced6;

    .empty-icon-agent {
      width: 48px;
      height: 48px;
    }

    .empty-icon-kb {
      width: 48px;
      height: 48px;
    }
  }

  .empty-text {
    font-size: 14px;
    color: #4e5969;
    margin: 0 0 8px;
  }

  .empty-subtext {
    font-size: 12px;
    color: #86909c;
    margin: 0;
  }

  &.small {
    padding: 24px 16px;
    .empty-text { margin: 0; }
  }
}

.shared-subsection {
  margin-top: 24px;
  padding-top: 24px;
  border-top: 1px solid #e5e7eb;

  .shared-subtitle {
    font-size: 14px;
    font-weight: 600;
    color: #1f2937;
    margin: 0 0 12px 0;
  }
}

// Join requests
.empty-join-requests {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48px 20px;

  .empty-icon {
    color: #07c05f;
    margin-bottom: 16px;
  }

  .empty-text {
    font-size: 14px;
    color: #86909c;
    margin: 0;
  }
}

.join-requests-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 400px;
  overflow-y: auto;

  .join-request-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 12px 16px;
    background: #f7f9fc;
    border-radius: 8px;
    transition: background 0.2s;

    &:hover {
      background: #f0f2f5;
    }

    .request-user {
      display: flex;
      align-items: flex-start;
      gap: 12px;
      flex: 1;
      min-width: 0;
    }

    .request-avatar {
      width: 36px;
      height: 36px;
      border-radius: 50%;
      background: rgba(7, 192, 95, 0.1);
      color: #07c05f;
      display: flex;
      align-items: center;
      justify-content: center;
      flex-shrink: 0;
    }

    .request-info {
      display: flex;
      flex-direction: column;
      gap: 2px;
      min-width: 0;

      .request-name {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 14px;
        font-weight: 500;
        color: #1f2937;

        .request-type-tag {
          flex-shrink: 0;
        }
      }

      .request-email {
        font-size: 12px;
        color: #86909c;
      }

      .request-prev-role {
        font-size: 12px;
        color: #ed702e;
        margin-top: 2px;
      }

      .request-message {
        font-size: 12px;
        color: #6b7280;
        margin: 4px 0 0;
        line-height: 1.4;
      }

      .request-requested-role {
        font-size: 12px;
        color: #6b7280;
        margin-top: 2px;
      }

      .request-time {
        font-size: 12px;
        color: #9ca3af;
        margin-top: 4px;
      }
    }

    .request-actions {
      display: flex;
      align-items: center;
      gap: 12px;
      flex-shrink: 0;

      .request-assign-role {
        display: flex;
        align-items: center;
        gap: 8px;

        .request-assign-label {
          font-size: 12px;
          color: #6b7280;
          white-space: nowrap;
        }
        .request-role-select {
          min-width: 100px;
        }
      }
    }
  }
}

.shared-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 400px;
  overflow-y: auto;

  .shared-item {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    background: #f7f9fc;
    border-radius: 8px;
    cursor: pointer;
    transition: all 0.2s;

    &:hover {
      background: #f0f5ff;
    }

    .shared-icon {
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 0 8px;
      height: 26px;
      border-radius: 6px;
      gap: 4px;

      &.type-document {
        background: rgba(7, 192, 95, 0.08);
        color: #059669;
      }

      &.type-faq {
        background: rgba(0, 82, 217, 0.08);
        color: #0052d9;
      }

      &      .shared-icon-org {
        background: rgba(7, 192, 95, 0.08);
        color: #059669;
      }

      &.shared-icon-agent-wrap {
        padding: 0;
        height: auto;
        background: transparent;
      }

      &.shared-icon-kb {
        background: rgba(7, 192, 95, 0.08);
        color: #059669;
      }

      .shared-icon-kb-img {
        width: 20px;
        height: 20px;
        flex-shrink: 0;
      }

      .shared-icon-agent {
        width: 20px;
        height: 20px;
        flex-shrink: 0;
      }

      .org-icon-img {
        width: 18px;
        height: 18px;
        flex-shrink: 0;
      }

      .badge-count {
        font-size: 12px;
        font-weight: 500;
      }
    }

    .shared-info {
      flex: 1;
      min-width: 0;

      .shared-name {
        display: block;
        font-size: 14px;
        font-weight: 500;
        color: #1d2129;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        margin-bottom: 4px;
      }

      .shared-meta {
        display: flex;
        align-items: center;
        gap: 12px;
        font-size: 12px;
        color: #86909c;

        .shared-by,
        .shared-time {
          display: flex;
          align-items: center;
          gap: 4px;
        }
      }

      .shared-desc {
        display: block;
        font-size: 12px;
        color: #86909c;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
    }

    .shared-permissions {
      display: flex;
      flex-direction: column;
      align-items: flex-end;
      gap: 6px;
      flex-shrink: 0;
      margin-left: auto;

      .perm-tag {
        white-space: nowrap;
      }
    }

    .shared-remove-btn {
      flex-shrink: 0;
      margin-left: 4px;
    }
  }
}

.settings-footer {
  padding: 20px 32px;
  border-top: 1px solid rgba(0, 0, 0, 0.06);
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  background: linear-gradient(0deg, #fafbfc 0%, #ffffff 100%);
}

// Transitions
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.35s cubic-bezier(0.4, 0, 0.2, 1);

  .settings-modal {
    transition: transform 0.35s cubic-bezier(0.34, 1.56, 0.64, 1);
  }
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.92) translateY(-8px);
  }
}

.modal-enter-to,
.modal-leave-from {
  .settings-modal {
    transform: scale(1) translateY(0);
  }
}

// 升级申请弹窗样式
.upgrade-dialog-content {
  .upgrade-current-role {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 16px;
    font-size: 14px;
    color: #4e5969;
  }

  .upgrade-form-item {
    margin-bottom: 16px;

    label {
      display: block;
      margin-bottom: 8px;
      font-size: 14px;
      font-weight: 500;
      color: #1d2129;
    }

    &:last-child {
      margin-bottom: 0;
    }
  }
}

.add-member-dialog {
  .add-member-tip {
    margin: 0 0 20px;
    padding: 10px 12px;
    background: #f7f8fa;
    border-radius: 6px;
    font-size: 13px;
    color: #4e5969;
    line-height: 1.5;
  }

  .add-member-field {
    margin-bottom: 20px;

    &:last-child {
      margin-bottom: 0;
    }

    label {
      display: block;
      margin-bottom: 8px;
      font-size: 14px;
      font-weight: 500;
      color: #1d2129;
    }

    .t-select {
      width: 100%;
    }

    .field-hint {
      margin: 6px 0 0;
      font-size: 12px;
      color: #86909c;
    }
  }
}
</style>

<style lang="less">
/* 共享智能体 hover 跟随气泡（Teleport 到 body） */
.agent-scope-popover-follow {
  position: fixed;
  z-index: 10000;
  pointer-events: none;
}

.agent-scope-popover-card {
  min-width: 220px;
  max-width: 280px;
  padding: 14px 16px;
  background: #fff;
  border-radius: 10px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12), 0 2px 8px rgba(0, 0, 0, 0.06);
  border: 1px solid #e5e7eb;
}

.agent-scope-popover-name {
  font-size: 14px;
  font-weight: 600;
  color: #1d2129;
  margin-bottom: 8px;
  line-height: 1.3;
  padding-right: 8px;
}

.agent-scope-popover-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 16px;
  font-size: 12px;
  color: #86909c;
  margin-bottom: 10px;

  .popover-meta-item {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
}

.agent-scope-popover-permission {
  font-size: 12px;
  margin-bottom: 10px;

  .popover-label {
    color: #86909c;
    margin-right: 6px;
  }

  .popover-value {
    color: #1d2129;
    font-weight: 500;
  }
}

.agent-scope-popover-divider {
  height: 1px;
  background: #f0f0f0;
  margin: 10px 0;
}

.agent-scope-popover-section-title {
  font-size: 12px;
  font-weight: 600;
  color: #1d2129;
  margin-bottom: 8px;
}

.agent-scope-popover-row {
  font-size: 12px;
  color: #4e5969;
  line-height: 1.7;
  padding: 2px 0;
}

.agent-scope-popover-fade-enter-active,
.agent-scope-popover-fade-leave-active {
  transition: opacity 0.12s ease;
}
.agent-scope-popover-fade-enter-from,
.agent-scope-popover-fade-leave-to {
  opacity: 0;
}
</style>
