<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="handleClose">
        <div class="settings-modal">
          <!-- 关闭按钮 -->
          <button class="close-btn" @click="handleClose" :aria-label="$t('common.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </button>

          <div class="settings-container">
            <!-- 左侧导航 -->
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ modalTitle }}</h2>
              </div>
              <div class="settings-nav">
                <template v-for="group in navGroups" :key="group.key">
                  <div class="nav-group-title">{{ group.label }}</div>
                  <div v-for="item in group.items" :key="item.key"
                    :class="['nav-item', { 'active': currentSection === item.key }]"
                    @click="currentSection = item.key">
                    <img v-if="item.key === 'sharedAgents'"
                      :src="currentSection === 'sharedAgents' ? agentIconActiveSrc : agentIconSrc"
                      class="nav-icon nav-icon-img" alt="" aria-hidden="true" />
                    <t-icon v-else :name="item.icon" class="nav-icon" />
                    <span class="nav-label">{{ item.label }}</span>
                    <span
                      v-if="item.badge != null && (item.key === 'sharedKb' || item.key === 'sharedAgents' ? true : item.badge > 0)"
                      :class="['nav-badge', { 'nav-badge-count': item.key === 'sharedKb' || item.key === 'sharedAgents' }]">{{
                        item.badge }}</span>
                  </div>
                </template>
              </div>
            </div>

            <!-- 右侧内容区域 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <!-- 组织管理员但空间角色不足，给出只读提示 -->
                <div v-if="showTenantRoleHint" class="tenant-role-hint">
                  <t-icon name="info-circle" size="16px" />
                  <span>{{ $t('organization.rbac.needTenantAdminTip') }}</span>
                </div>
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
                          <t-popup v-model="avatarPopoverVisible" trigger="click" placement="bottom-left"
                            :disabled="!isAdmin" overlay-class-name="avatar-emoji-popover">
                            <div class="avatar-trigger-wrap">
                              <SpaceAvatar :name="formData.name || '?'" :avatar="formData.avatar" size="medium" />
                              <span v-if="isAdmin" class="avatar-change-hint">{{ $t('organization.avatar') }}</span>
                            </div>
                            <template #content>
                              <div class="avatar-popover-content" @click.stop>
                                <p class="avatar-popover-title">{{ $t('organization.avatarPickerHint') }}</p>
                                <div class="avatar-emoji-grid">
                                  <button v-for="emoji in avatarEmojiOptions" :key="emoji" type="button"
                                    class="avatar-emoji-btn"
                                    :class="{ 'is-selected': formData.avatar === 'emoji:' + emoji }"
                                    @click="selectAvatarEmoji(emoji)">
                                    {{ emoji }}
                                  </button>
                                </div>
                                <t-button v-if="formData.avatar" variant="text" size="small" class="avatar-clear-btn"
                                  @click="clearAvatarEmoji">
                                  {{ $t('organization.avatarClear') }}
                                </t-button>
                              </div>
                            </template>
                          </t-popup>
                          <t-input v-model="formData.name" :placeholder="$t('organization.namePlaceholder')"
                            :disabled="!isAdmin" class="name-input" />
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
                        <t-textarea v-model="formData.description"
                          :placeholder="$t('organization.descriptionPlaceholder')"
                          :autosize="{ minRows: 3, maxRows: 6 }" :maxlength="500" :disabled="!isAdmin" />
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
                                  <t-button variant="text" size="small" @click="refreshInviteCode"
                                    :loading="refreshingCode">
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
                              <span class="invite-method-title">{{ $t('organization.settings.inviteLinkValidity')
                              }}</span>
                            </div>
                            <p class="invite-validity-desc">{{ $t('organization.settings.inviteLinkValidityDesc') }}</p>
                            <t-select v-model="formData.invite_code_validity_days" :options="inviteValidityOptions"
                              size="small" class="invite-validity-select" :disabled="!isAdmin"
                              @change="handleValidityChange" />
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
                              <t-switch v-model="formData.require_approval" @change="handleApprovalToggle" />
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
                              <t-switch v-model="formData.searchable" @change="handleSearchableToggle" />
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
                              <t-input-number v-model="formData.member_limit" :min="0" :max="10000"
                                :placeholder="$t('organization.settings.memberLimitPlaceholder')" theme="normal"
                                style="width: 140px;" />
                              <span class="member-limit-hint">{{ $t('organization.settings.memberLimitHint', {
                                count:
                                  orgInfo?.member_count
                                  ?? 0
                              }) }}</span>
                            </div>
                          </div>
                        </div>
                      </div>
                    </div>


                  </div>
                </div>

                <!-- 创建空间 - 权限说明 -->
                <div v-if="isCreateMode" v-show="currentSection === 'permissions'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.editor.permissionsTitle') }}</h2>
                    <p class="section-description">{{ $t('organization.editor.permissionsDesc') }}</p>
                  </div>

                  <div class="permissions-info">
                    <div class="permission-card">
                      <div class="permission-header">
                        <div class="permission-icon admin">
                          <t-icon name="user-safety" />
                        </div>
                        <div class="permission-title">
                          <span class="role-name">{{ $t('organization.role.admin') }}</span>
                          <t-tag size="small" theme="primary">{{ $t('organization.editor.fullAccess') }}</t-tag>
                        </div>
                      </div>
                      <ul class="permission-list">
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.adminPerm1') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.adminPerm2') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.adminPerm3') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.adminPerm4') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.useSharedAgentsPerm') }}</li>
                      </ul>
                    </div>
                    <div class="permission-card">
                      <div class="permission-header">
                        <div class="permission-icon editor">
                          <t-icon name="edit" />
                        </div>
                        <div class="permission-title">
                          <span class="role-name">{{ $t('organization.role.editor') }}</span>
                          <t-tag size="small" theme="warning">{{ $t('organization.editor.editAccess') }}</t-tag>
                        </div>
                      </div>
                      <ul class="permission-list">
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.editorPerm1') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.editorPerm2') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.useSharedAgentsPerm') }}</li>
                        <li><t-icon name="close" class="close-icon" />{{ $t('organization.editor.shareKBPerm') }}</li>
                        <li><t-icon name="close" class="close-icon" />{{ $t('organization.editor.editorPerm3') }}</li>
                      </ul>
                    </div>
                    <div class="permission-card">
                      <div class="permission-header">
                        <div class="permission-icon viewer">
                          <t-icon name="browse" />
                        </div>
                        <div class="permission-title">
                          <span class="role-name">{{ $t('organization.role.viewer') }}</span>
                          <t-tag size="small">{{ $t('organization.editor.viewAccess') }}</t-tag>
                        </div>
                      </div>
                      <ul class="permission-list">
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.viewerPerm1') }}</li>
                        <li><t-icon name="check" class="check-icon" />{{ $t('organization.editor.useSharedAgentsPerm') }}</li>
                        <li><t-icon name="close" class="close-icon" />{{ $t('organization.editor.shareKBPerm') }}</li>
                        <li><t-icon name="close" class="close-icon" />{{ $t('organization.editor.viewerPerm2') }}</li>
                        <li><t-icon name="close" class="close-icon" />{{ $t('organization.editor.viewerPerm3') }}</li>
                      </ul>
                    </div>
                  </div>
                  <div class="info-notice">
                    <t-icon name="info-circle" />
                    <span>{{ $t('organization.editor.ownerNote') }}</span>
                  </div>
                </div>

                <!-- 成员管理 -->
                <div v-show="currentSection === 'members'" class="section">
                  <div class="section-header">
                    <div class="section-header-row">
                      <div class="section-header-titlewrap">
                        <h2>{{ $t('organization.manageMembers') }}</h2>
                        <t-popup placement="bottom-start" trigger="hover"
                          overlay-class-name="org-permissions-popup-overlay"
                          :overlay-inner-style="permissionsPopupInnerStyle">
                          <button type="button" class="permissions-trigger-btn"
                            :aria-label="$t('organization.editor.permissionsTitle')"
                            :title="$t('organization.settings.permissionsIconHint')">
                            <t-icon name="info-circle" size="16px" />
                          </button>
                          <template #content>
                            <div class="permissions-compact permissions-compact--popover">
                              <div class="permissions-compact-header">
                                <span class="permissions-compact-title">{{ $t('organization.editor.permissionsTitle') }}</span>
                                <span class="permissions-compact-desc">{{ $t('organization.editor.permissionsDesc') }}</span>
                              </div>
                              <div class="permissions-compact-grid">
                                <div v-for="role in orgRoleMatrixOrder" :key="role"
                                  :class="['perm-role-block', role, { 'is-me': orgInfo?.my_role === role }]">
                                  <div class="perm-role-tag">
                                    <t-icon :name="orgRoleIcon(role)" size="12px" />
                                    <span>{{ $t(`organization.role.${role}`) }}</span>
                                    <span v-if="orgInfo?.my_role === role" class="me-badge">{{ $t('common.me') }}</span>
                                  </div>
                                  <div class="perm-items">
                                    <span v-for="(perm, idx) in orgRoleMatrix[role]" :key="idx"
                                      :class="['perm-item', perm.has ? 'has' : 'no']">
                                      <t-icon :name="perm.has ? 'check' : 'close'" size="12px" />
                                      {{ $t(`organization.editor.${perm.key}`) }}
                                    </span>
                                  </div>
                                </div>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </div>
                    <p class="section-description">{{ $t('organization.settings.membersDesc') }}</p>
                  </div>

                  <div class="members-list-wrap">
                    <div class="members-list-header">
                      <div class="members-list-titlewrap">
                        <span class="members-list-title">{{ $t('organization.members.listTitle') }}</span>
                        <span class="members-list-count-badge">{{ filteredMembers.length }}</span>
                      </div>
                      <div class="members-list-actions">
                        <div class="members-list-search">
                          <t-input v-model="memberSearchQuery" size="small"
                            :placeholder="$t('organization.members.searchPlaceholder')" clearable>
                            <template #prefix-icon>
                              <t-icon name="search" />
                            </template>
                          </t-input>
                        </div>
                        <t-popup v-if="canRequestUpgrade" v-model="upgradePopupVisible" trigger="click"
                          placement="bottom-end" destroy-on-close overlay-class-name="org-upgrade-popup-overlay">
                          <t-button variant="outline" shape="square" size="small" class="members-list-upgrade-btn"
                            :disabled="hasPendingUpgrade"
                            :title="hasPendingUpgrade ? $t('organization.upgrade.pending') : $t('organization.upgrade.requestUpgrade')"
                            :aria-label="hasPendingUpgrade ? $t('organization.upgrade.pending') : $t('organization.upgrade.requestUpgrade')">
                            <template #icon><t-icon name="arrow-up" /></template>
                          </t-button>
                          <template #content>
                            <div class="org-upgrade-popup-inner" @click.stop>
                              <div class="member-invite-popup-title">{{ $t('organization.upgrade.dialogTitle') }}</div>
                              <p class="add-member-tip">{{ $t('organization.upgrade.dialogDesc') }}</p>

                              <div class="upgrade-current-role-bar">
                                <span class="upgrade-current-role-label">{{ $t('organization.upgrade.currentRole') }}</span>
                                <t-tag size="small" :theme="getRoleTheme(orgInfo?.my_role || 'viewer')" variant="light">
                                  {{ $t(`organization.role.${orgInfo?.my_role || 'viewer'}`) }}
                                </t-tag>
                              </div>

                              <div class="org-upgrade-fields">
                                <div class="org-upgrade-field">
                                  <label class="org-upgrade-field-label">{{ $t('organization.upgrade.selectRole') }}</label>
                                  <div class="upgrade-role-pills">
                                    <button v-for="opt in upgradeRoleOptions" :key="opt.value" type="button"
                                      :class="['upgrade-role-pill', { active: upgradeForm.requested_role === opt.value }]"
                                      @click="upgradeForm.requested_role = opt.value as 'editor' | 'admin'">
                                      {{ opt.label }}
                                    </button>
                                  </div>
                                </div>
                                <div class="org-upgrade-field org-upgrade-field--last">
                                  <label class="org-upgrade-field-label">{{ $t('organization.upgrade.reason') }}</label>
                                  <t-textarea v-model="upgradeForm.message" size="medium"
                                    :placeholder="$t('organization.upgrade.reasonPlaceholder')"
                                    :autosize="{ minRows: 2, maxRows: 4 }" :maxlength="500" />
                                </div>
                              </div>

                              <div class="invite-popup-footer">
                                <t-button variant="outline" :disabled="upgradeSubmitting"
                                  @click="upgradePopupVisible = false">
                                  {{ $t('common.cancel') }}
                                </t-button>
                                <t-button theme="primary" :loading="upgradeSubmitting" @click="handleSubmitUpgrade">
                                  {{ $t('organization.upgrade.submitBtn') }}
                                </t-button>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                        <t-popup v-if="isAdmin" v-model="addMemberPopupVisible" trigger="click" placement="bottom-end"
                          destroy-on-close overlay-class-name="org-add-member-popup-overlay">
                          <t-button theme="primary" variant="outline" shape="square" size="small"
                            class="members-list-add-btn" :title="$t('organization.addMember.button')"
                            :aria-label="$t('organization.addMember.button')">
                            <template #icon><t-icon name="user-add" /></template>
                          </t-button>
                          <template #content>
                            <div class="member-invite-popup-inner" @click.stop>
                              <div class="member-invite-popup-title">{{ $t('organization.addMember.dialogTitle') }}</div>
                              <p class="add-member-tip">{{ $t('organization.addMember.tipTenant') }}</p>
                              <t-form layout="vertical" class="member-invite-form">
                                <t-form-item :label="$t('organization.addMember.searchTenant')">
                                  <div class="member-form-control">
                                    <t-select v-model="selectedTenantId"
                                      :placeholder="$t('organization.addMember.searchTenantPlaceholder')" filterable
                                      :filter="() => true" :loading="tenantSearchLoading" @search="handleTenantSearch"
                                      clearable :options="tenantSearchOptions" />
                                    <p class="field-hint">{{ $t('organization.addMember.searchTenantHint') }}</p>
                                  </div>
                                </t-form-item>
                                <t-form-item :label="$t('organization.addMember.selectRole')">
                                  <t-select v-model="addMemberRole" :options="addMemberRoleOptions"
                                    :placeholder="$t('organization.addMember.selectRole')" />
                                </t-form-item>
                              </t-form>
                              <div class="invite-popup-footer">
                                <t-button variant="outline" :disabled="addMemberSubmitting"
                                  @click="addMemberPopupVisible = false">
                                  {{ $t('common.cancel') }}
                                </t-button>
                                <t-button theme="primary" :loading="addMemberSubmitting"
                                  :disabled="selectedTenantId == null" @click="handleAddMember">
                                  {{ $t('organization.addMember.confirmBtn') }}
                                </t-button>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </div>

                    <div v-if="membersLoading && members.length === 0" class="loading-inline">
                      <t-loading size="small" />
                      <span>{{ $t('organization.members.loading') }}</span>
                    </div>
                    <div v-else-if="filteredMembers.length === 0" class="empty-state">
                      <t-empty :description="memberSearchQuery.trim()
                        ? $t('organization.members.emptySearch', { q: memberSearchQuery })
                        : $t('organization.noMembers')" />
                    </div>
                    <div v-else class="data-table-shell">
                      <t-table row-key="id" :data="filteredMembers" :columns="memberColumns" size="medium" hover
                        stripe :loading="membersLoading">
                        <template #member="{ row }">
                          <div class="member-cell">
                            <span class="member-name">
                              {{ memberPrimaryLabel(row) }}
                              <span v-if="isOwnerMember(row)" class="owner-tag">{{ $t('organization.owner') }}</span>
                              <span v-if="row.user_id === authStore.currentUserId" class="me-tag">{{ $t('common.me')
                              }}</span>
                            </span>
                            <span v-if="memberSecondaryLabel(row)" class="member-email">{{ memberSecondaryLabel(row)
                            }}</span>
                          </div>
                        </template>
                        <template #role="{ row }">
                          <div class="role-cell">
                            <t-select v-if="isAdmin && !isOwnerMember(row)" :model-value="row.role"
                              class="member-role-select" size="small" :options="roleOptions"
                              @change="(val: string) => handleRoleChange(row, val)" />
                            <t-tag v-else size="small" :theme="getRoleTheme(row.role)">
                              {{ $t(`organization.role.${row.role}`) }}
                            </t-tag>
                          </div>
                        </template>
                        <template #joined_at="{ row }">{{ formatDate(row.joined_at) }}</template>
                        <template #actions="{ row }">
                          <t-popconfirm v-if="isAdmin && !isOwnerMember(row)"
                            :content="$t('organization.detail.removeMemberConfirm', { name: memberPrimaryLabel(row) })"
                            :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                            :cancel-btn="{ content: $t('common.cancel') }" placement="left"
                            @confirm="confirmRemoveMember(row)">
                            <t-tooltip :content="$t('organization.detail.removeMember')" placement="top">
                              <t-button theme="danger" shape="square" variant="text" size="small" @click.stop>
                                <template #icon><t-icon name="user-clear" /></template>
                              </t-button>
                            </t-tooltip>
                          </t-popconfirm>
                        </template>
                      </t-table>
                    </div>
                  </div>
                </div>

                <!-- 加入申请（待审核） -->
                <div v-show="currentSection === 'joinRequests'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('organization.settings.joinRequests') }}</h2>
                    <p class="section-description">{{ $t('organization.settings.joinRequestsDesc') }}</p>
                  </div>

                  <div class="members-list-wrap join-requests-wrap">
                    <div class="members-list-header">
                      <div class="members-list-titlewrap">
                        <span class="members-list-title">{{ $t('organization.joinRequests.listTitle') }}</span>
                        <span class="members-list-count-badge">{{ filteredJoinRequests.length }}</span>
                      </div>
                      <div class="members-list-actions">
                        <div class="members-list-search">
                          <t-input v-model="joinRequestSearchQuery" size="small"
                            :placeholder="$t('organization.joinRequests.searchPlaceholder')" clearable>
                            <template #prefix-icon>
                              <t-icon name="search" />
                            </template>
                          </t-input>
                        </div>
                      </div>
                    </div>

                    <div v-if="joinRequestsLoading && joinRequests.length === 0" class="loading-inline">
                      <t-loading size="small" />
                      <span>{{ $t('organization.joinRequests.loading') }}</span>
                    </div>
                    <div v-else-if="filteredJoinRequests.length === 0" class="empty-state">
                      <t-empty :description="joinRequestSearchQuery.trim()
                        ? $t('organization.joinRequests.emptySearch', { q: joinRequestSearchQuery })
                        : $t('organization.settings.noPendingRequests')" />
                    </div>
                    <div v-else class="data-table-shell join-requests-table">
                      <t-table row-key="id" :data="filteredJoinRequests" :columns="joinRequestColumns" size="medium"
                        hover stripe :loading="joinRequestsLoading">
                        <template #applicant="{ row }">
                          <div class="member-cell">
                            <span class="member-name">{{ joinRequestApplicantLabel(row) }}</span>
                            <span v-if="joinRequestApplicantSecondary(row)" class="member-email">
                              {{ joinRequestApplicantSecondary(row) }}
                            </span>
                          </div>
                        </template>
                        <template #request_type="{ row }">
                          <t-tag size="small" :theme="row.request_type === 'upgrade' ? 'warning' : 'primary'"
                            variant="light">
                            {{ row.request_type === 'upgrade'
                              ? $t('organization.joinRequests.typeUpgrade')
                              : $t('organization.joinRequests.typeJoin') }}
                          </t-tag>
                        </template>
                        <template #requested_role="{ row }">
                          <span v-if="row.request_type === 'upgrade' && row.prev_role" class="join-request-role-change">
                            {{ roleLabel(row.prev_role) }}
                            <t-icon name="arrow-right" size="12px" />
                            {{ roleLabel(row.requested_role) }}
                          </span>
                          <t-tag v-else size="small" :theme="getRoleTheme(row.requested_role)" variant="light">
                            {{ roleLabel(row.requested_role) }}
                          </t-tag>
                        </template>
                        <template #message="{ row }">
                          <span class="join-request-message" :title="row.message || undefined">
                            {{ row.message || '—' }}
                          </span>
                        </template>
                        <template #created_at="{ row }">{{ formatDate(row.created_at) }}</template>
                        <template #actions="{ row }">
                          <div class="join-request-actions">
                            <t-popup :visible="approvePopupRequestId === row.id"
                              placement="left-start" destroy-on-close overlay-class-name="org-approve-request-popup-overlay"
                              @visible-change="(visible: boolean) => handleApprovePopupVisibleChange(visible, row)">
                              <t-tooltip :content="$t('organization.settings.approve')" placement="top">
                                <t-button theme="primary" variant="text" shape="square" size="small"
                                  :loading="reviewingRequestId === row.id" @click.stop="openApprovePopup(row)">
                                  <template #icon><t-icon name="check" /></template>
                                </t-button>
                              </t-tooltip>
                              <template #content>
                                <div class="org-approve-request-popup-inner" @click.stop>
                                  <div class="member-invite-popup-title">{{ $t('organization.joinRequests.approveTitle') }}</div>
                                  <p class="add-member-tip">
                                    {{ $t('organization.joinRequests.approveDesc', { name: joinRequestApplicantLabel(row) }) }}
                                  </p>
                                  <div class="org-upgrade-field org-upgrade-field--last">
                                    <label class="org-upgrade-field-label">{{ $t('organization.settings.assignRole') }}</label>
                                    <t-select v-model="approveAssignRole" size="medium" :options="orgRoleOptions" />
                                  </div>
                                  <div class="invite-popup-footer">
                                    <t-button variant="outline" :disabled="reviewingRequestId === row.id"
                                      @click="closeApprovePopup">
                                      {{ $t('common.cancel') }}
                                    </t-button>
                                    <t-button theme="primary" :loading="reviewingRequestId === row.id"
                                      @click="confirmApproveRequest(row)">
                                      {{ $t('organization.settings.approve') }}
                                    </t-button>
                                  </div>
                                </div>
                              </template>
                            </t-popup>
                            <t-popconfirm :content="$t('organization.joinRequests.rejectConfirm')"
                              :confirm-btn="{ content: $t('organization.settings.reject'), theme: 'danger' }"
                              :cancel-btn="{ content: $t('common.cancel') }" placement="left"
                              @confirm="handleRejectRequest(row)">
                              <t-tooltip :content="$t('organization.settings.reject')" placement="top">
                                <t-button theme="danger" variant="text" shape="square" size="small"
                                  :loading="reviewingRequestId === row.id" @click.stop>
                                  <template #icon><t-icon name="close" /></template>
                                </t-button>
                              </t-tooltip>
                            </t-popconfirm>
                          </div>
                        </template>
                      </t-table>
                    </div>
                  </div>
                </div>

                <!-- 共享知识库 -->
                <div v-show="currentSection === 'sharedKb'" class="section">
                  <div class="section-header">
                    <div class="section-header-row">
                      <div class="section-header-titlewrap">
                        <h2>{{ $t('organization.share.sharedKnowledgeBase') }}</h2>
                        <t-popup placement="bottom-start" trigger="hover"
                          overlay-class-name="org-permissions-popup-overlay"
                          :overlay-inner-style="permissionsHintPopupInnerStyle">
                          <button type="button" class="permissions-trigger-btn"
                            :aria-label="$t('organization.settings.permissionCalcFormula')"
                            :title="$t('organization.settings.permissionCalcFormula')">
                            <t-icon name="info-circle" size="16px" />
                          </button>
                          <template #content>
                            <div class="permission-hint-popover">
                              <p class="permission-hint-title">{{ $t('organization.settings.sharePermissionLabel') }}</p>
                              <p class="permission-hint-desc">{{ $t('organization.settings.permissionCalcTip') }}</p>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </div>
                    <p class="section-description">{{ $t('organization.settings.sharedDesc') }}</p>
                  </div>

                  <div class="shared-resources-wrap">
                    <div class="members-list-header">
                      <div class="members-list-titlewrap">
                        <span class="members-list-title">{{ $t('organization.sharedResources.kbListTitle') }}</span>
                        <span class="members-list-count-badge">{{ sharedKnowledgeBases.length }}</span>
                      </div>
                    </div>

                    <div v-if="sharesLoading && sharedKnowledgeBases.length === 0" class="loading-inline">
                      <t-loading size="small" />
                      <span>{{ $t('organization.sharedResources.loading') }}</span>
                    </div>
                    <div v-else-if="sharedKnowledgeBases.length === 0" class="empty-state">
                      <t-empty>
                        <template #description>
                          <p class="empty-state-title">{{ $t('organization.settings.noSharedKB') }}</p>
                          <p class="empty-state-desc">{{ $t('organization.settings.noSharedKBTip') }}</p>
                        </template>
                      </t-empty>
                    </div>
                    <div v-else class="data-table-shell shared-resources-table">
                      <t-table row-key="id" :data="sharedKnowledgeBases" :columns="sharedKbColumns" size="medium"
                        hover stripe :loading="sharesLoading" class="shared-kb-table">
                        <template #name="{ row }">
                          <span class="resource-name" :title="row.knowledge_base_name">{{ row.knowledge_base_name }}</span>
                        </template>
                        <template #shared_by="{ row }">
                          <span class="resource-meta">{{ row.shared_by_username || '—' }}</span>
                        </template>
                        <template #created_at="{ row }">{{ formatDate(row.created_at) }}</template>
                        <template #space_permission="{ row }">
                          <t-tag size="small" :theme="getPermissionTheme(row.permission)" variant="light">
                            {{ sharePermissionLabel(row.permission) }}
                          </t-tag>
                        </template>
                        <template #my_permission="{ row }">
                          <t-tag size="small"
                            :theme="getPermissionTheme(row.my_permission ?? row.permission)" variant="light">
                            {{ sharePermissionLabel(row.my_permission ?? row.permission) }}
                          </t-tag>
                        </template>
                        <template #actions="{ row }">
                          <div class="resource-row-actions">
                            <t-tooltip :content="$t('knowledgeList.detail.goToKb')" placement="top">
                              <t-button theme="primary" shape="square" variant="text" size="small"
                                :aria-label="$t('knowledgeList.detail.goToKb')"
                                @click.stop="handleShareClick(row)">
                                <template #icon><t-icon name="browse" /></template>
                              </t-button>
                            </t-tooltip>
                            <t-popconfirm v-if="isAdmin"
                            :content="$t('organization.settings.removeShareConfirm', { name: row.knowledge_base_name || row.knowledge_base_id })"
                            :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                            :cancel-btn="{ content: $t('common.cancel') }" placement="left"
                            @confirm="handleRemoveShare(row)">
                            <t-tooltip :content="$t('organization.settings.removeShareFromOrg')" placement="top">
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

                <!-- 共享智能体 -->
                <div v-show="currentSection === 'sharedAgents'" class="section">
                  <div class="section-header">
                    <div class="section-header-row">
                      <div class="section-header-titlewrap">
                        <h2>{{ $t('organization.settings.sharedAgents') }}</h2>
                        <t-popup placement="bottom-start" trigger="hover"
                          overlay-class-name="org-permissions-popup-overlay"
                          :overlay-inner-style="permissionsHintPopupInnerStyle">
                          <button type="button" class="permissions-trigger-btn"
                            :aria-label="$t('organization.settings.sharedAgentsKbHintShort')"
                            :title="$t('organization.settings.sharedAgentsKbHintShort')">
                            <t-icon name="info-circle" size="16px" />
                          </button>
                          <template #content>
                            <div class="permission-hint-popover">
                              <p class="permission-hint-title">{{ $t('organization.settings.sharedAgents') }}</p>
                              <p class="permission-hint-desc">{{ $t('organization.settings.sharedAgentsKbHint') }}</p>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </div>
                    <p class="section-description">{{ $t('organization.settings.sharedAgentsDesc') }}</p>
                  </div>

                  <div class="shared-resources-wrap">
                    <div class="members-list-header">
                      <div class="members-list-titlewrap">
                        <span class="members-list-title">{{ $t('organization.sharedResources.agentListTitle') }}</span>
                        <span class="members-list-count-badge">{{ sharedAgents.length }}</span>
                      </div>
                    </div>

                    <div v-if="sharedAgents.length === 0" class="empty-state">
                      <t-empty>
                        <template #description>
                          <p class="empty-state-title">{{ $t('organization.settings.noSharedAgents') }}</p>
                          <p class="empty-state-desc">{{ $t('organization.settings.noSharedAgentsTip') }}</p>
                        </template>
                      </t-empty>
                    </div>
                    <div v-else class="data-table-shell shared-resources-table">
                      <t-table row-key="id" :data="sharedAgents" :columns="sharedAgentColumns" size="medium" hover
                        stripe class="shared-agent-table">
                        <template #name="{ row }">
                          <span class="resource-name" :title="row.agent_name || row.agent_id">{{ row.agent_name ||
                            row.agent_id }}</span>
                        </template>
                        <template #shared_by="{ row }">
                          <span class="resource-meta">{{ row.shared_by_username || '—' }}</span>
                        </template>
                        <template #created_at="{ row }">{{ formatDate(row.created_at) }}</template>
                        <template #scope_kb="{ row }">
                          <span class="resource-meta" :title="agentKbScopeLabel(row)">{{ agentKbScopeLabel(row) }}</span>
                        </template>
                        <template #scope_web_search="{ row }">
                          <span class="resource-meta">{{ agentWebSearchScopeLabel(row) }}</span>
                        </template>
                        <template #scope_mcp="{ row }">
                          <span class="resource-meta" :title="agentMcpScopeLabel(row)">{{ agentMcpScopeLabel(row) }}</span>
                        </template>
                        <template #permission>
                          <t-tag size="small" theme="default" variant="light">
                            {{ $t('organization.share.permissionReadonly') }}
                          </t-tag>
                        </template>
                        <template #actions="{ row }">
                          <t-popconfirm v-if="isAdmin"
                            :content="$t('organization.settings.removeAgentShareConfirm', { name: row.agent_name || row.agent_id })"
                            :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
                            :cancel-btn="{ content: $t('common.cancel') }" placement="left"
                            @confirm="handleRemoveAgentShare(row)">
                            <t-tooltip :content="$t('organization.settings.removeShareFromOrg')" placement="top">
                              <t-button theme="danger" shape="square" variant="text" size="small" @click.stop>
                                <template #icon><t-icon name="delete" /></template>
                              </t-button>
                            </t-tooltip>
                          </t-popconfirm>
                        </template>
                      </t-table>
                    </div>
                  </div>
                </div>

              </div>

              <!-- 底部操作按钮 -->
              <div class="settings-footer">
                <t-button variant="outline" @click="handleClose">{{ $t('common.cancel') }}</t-button>
                <t-button v-if="isAdmin" theme="primary" :loading="submitting" @click="handleSave">
                  {{ isCreateMode ? $t('common.create') : $t('common.save') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  getOrganization,
  listOrgShares,
  listOrgAgentShares,
  listJoinRequests,
  searchTenantsForInvite,
  type OrganizationMember,
  type KnowledgeBaseShare,
  type AgentShareResponse,
  type JoinRequestResponse,
  type TenantInviteCandidate
} from '@/api/organization'
import { useOrganizationStore } from '@/stores/organization'
import { useAuthStore } from '@/stores/auth'
import SpaceAvatar from '@/components/SpaceAvatar.vue'
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
const orgInfo = computed(() => orgStore.currentOrganization)
const members = computed(() => orgStore.currentMembers)
const sharedKnowledgeBases = ref<KnowledgeBaseShare[]>([])
const sharedAgents = ref<AgentShareResponse[]>([])
const joinRequests = ref<JoinRequestResponse[]>([])
const joinRequestsLoading = ref(false)
const joinRequestSearchQuery = ref('')
const reviewingRequestId = ref<string | null>(null)
const sharesLoading = ref(false)
const membersLoading = ref(false)
const memberSearchQuery = ref('')
const submitting = ref(false)
const refreshingCode = ref(false)
const inviteCode = ref('')
const inviteCodeExpiresAt = ref<string | null>(null)
const upgradePopupVisible = ref(false)
const upgradeSubmitting = ref(false)
const hasPendingUpgrade = ref(false)
const upgradeForm = ref({
  requested_role: 'editor' as 'admin' | 'editor' | 'viewer',
  message: ''
})

// 添加成员（按空间邀请）相关状态。Plan 3 之后，邀请实际上是把
// 一整个空间拉进空间；这里的「搜索结果」是空间候选列表，每条带一个
// 代表用户用于展示。`selectedTenantId` 是真正提交给后端的 tenant_id。
const addMemberPopupVisible = ref(false)
const addMemberSubmitting = ref(false)
const tenantSearchLoading = ref(false)
const tenantSearchResults = ref<TenantInviteCandidate[]>([])
const selectedTenantId = ref<number | null>(null)
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
// 后端组织相关变更接口（保存设置、邀请、搜索用户、改/删成员、审核加入申请、
// 升级申请、刷新邀请码、移除共享等）在路由层都要求当前空间角色 ≥ admin（见
// internal/router/router.go 的 RegisterOrganizationRoutes）。跨空间超管可绕过。
// 因此前端任何"管理类"入口必须同时满足：组织内是 admin/owner ∩ 当前空间 admin+。
const hasTenantAdmin = computed(
  () => authStore.hasRole('admin') || authStore.canAccessAllTenants
)
const isAdmin = computed(() => {
  if (isCreateMode.value) return hasTenantAdmin.value
  const orgAdmin = orgInfo.value?.my_role === 'admin' || orgInfo.value?.is_owner
  return !!orgAdmin && hasTenantAdmin.value
})

// 当用户在组织内是 admin/owner 但当前空间角色不足时，展示只读提示
const showTenantRoleHint = computed(() => {
  if (isCreateMode.value) return !hasTenantAdmin.value
  const orgAdmin = orgInfo.value?.my_role === 'admin' || orgInfo.value?.is_owner
  return !!orgAdmin && !hasTenantAdmin.value
})

// 是否可以申请权限升级（非管理员成员可申请；后端也要求空间 admin+）
const canRequestUpgrade = computed(() => {
  if (isCreateMode.value || !props.orgId) return false
  const myRole = orgInfo.value?.my_role
  if (!myRole || myRole === 'admin') return false
  return hasTenantAdmin.value
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

// 空间搜索结果选项。主标签展示空间名，括号里附带代表用户名（不再展示
// 邮箱、不带"代表："前缀，避免冗长和译文别扭）；空间名缺失时回退到
// 代表用户名 / 空间 ID。
const tenantSearchOptions = computed(() =>
  tenantSearchResults.value.map((c) => {
    const tenantLabel = c.tenant_name || c.representative_username || `tenant#${c.tenant_id}`
    const showsTenantName = !!c.tenant_name
    const label =
      showsTenantName && c.representative_username
        ? `${tenantLabel}（${c.representative_username}）`
        : tenantLabel
    return { label, value: c.tenant_id }
  })
)

const modalTitle = computed(() => {
  if (isCreateMode.value) return t('organization.createOrg')
  return t('organization.settings.editTitle')
})

const navItems = computed(() => {
  const items: { key: string; icon: string; label: string; badge?: number }[] = [
    { key: 'basic', icon: 'info-circle', label: t('organization.editor.navBasic') },
  ]
  if (isCreateMode.value) {
    items.push({ key: 'permissions', icon: 'user-safety', label: t('organization.editor.navPermissions') })
  }
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

const navGroups = computed(() => {
  const itemMap = new Map(navItems.value.map((item) => [item.key, item]))
  const pickItems = (keys: string[]) =>
    keys.map((key) => itemMap.get(key)).filter(Boolean) as typeof navItems.value
  if (isCreateMode.value) {
    return [
      {
        key: 'basic',
        label: t('organization.navGroups.basic'),
        items: pickItems(['basic', 'permissions']),
      },
    ].filter((group) => group.items.length > 0)
  }
  return [
    {
      key: 'basic',
      label: t('organization.navGroups.basic'),
      items: pickItems(['basic']),
    },
    {
      key: 'management',
      label: t('organization.navGroups.management'),
      items: pickItems(['members', 'joinRequests']),
    },
    {
      key: 'resources',
      label: t('organization.navGroups.resources'),
      items: pickItems(['sharedKb', 'sharedAgents']),
    },
  ].filter((group) => group.items.length > 0)
})

const roleOptions = computed(() => [
  { label: t('organization.role.admin'), value: 'admin' },
  { label: t('organization.role.editor'), value: 'editor' },
  { label: t('organization.role.viewer'), value: 'viewer' }
])

const permissionsPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(520px, calc(100vw - 24px))',
  maxWidth: 'min(520px, calc(100vw - 24px))',
  maxHeight: 'min(400px, 65vh)',
  overflow: 'hidden',
}

const permissionsHintPopupInnerStyle = {
  boxSizing: 'border-box' as const,
  padding: '0',
  width: 'min(400px, calc(100vw - 24px))',
  maxWidth: 'min(400px, calc(100vw - 24px))',
  maxHeight: 'min(280px, 65vh)',
  overflow: 'hidden',
}

type OrgRole = 'admin' | 'editor' | 'viewer'
type OrgRolePerm = { key: string; has: boolean }

const orgRoleMatrixOrder: OrgRole[] = ['admin', 'editor', 'viewer']

const orgRoleMatrix: Record<OrgRole, OrgRolePerm[]> = {
  admin: [
    { key: 'viewerPerm1', has: true },
    { key: 'editorPerm1', has: true },
    { key: 'useSharedAgentsPerm', has: true },
    { key: 'shareKBPerm', has: true },
    { key: 'adminPerm1', has: true },
  ],
  editor: [
    { key: 'viewerPerm1', has: true },
    { key: 'editorPerm1', has: true },
    { key: 'useSharedAgentsPerm', has: true },
    { key: 'shareKBPerm', has: false },
    { key: 'adminPerm1', has: false },
  ],
  viewer: [
    { key: 'viewerPerm1', has: true },
    { key: 'editorPerm1', has: false },
    { key: 'useSharedAgentsPerm', has: true },
    { key: 'shareKBPerm', has: false },
    { key: 'adminPerm1', has: false },
  ],
}

function orgRoleIcon(role: OrgRole): string {
  switch (role) {
    case 'admin':
      return 'user-safety'
    case 'editor':
      return 'edit'
    default:
      return 'browse'
  }
}

const memberColumns = computed(() => {
  const cols = [
    { colKey: 'member', title: t('organization.members.columns.member'), ellipsis: true, minWidth: 160 },
    { colKey: 'role', title: t('organization.members.columns.role'), width: 132 },
    { colKey: 'joined_at', title: t('organization.members.columns.joinedAt'), width: 154 },
  ]
  if (isAdmin.value) {
    cols.push({ colKey: 'actions', title: t('organization.members.columns.operations'), width: 88, align: 'left' } as typeof cols[number])
  }
  return cols
})

function sharePermissionLabel(permission: string): string {
  if (permission === 'editor' || permission === 'admin') {
    return t('organization.share.permissionEditable')
  }
  return t('organization.share.permissionReadonly')
}

const joinRequestColumns = computed(() => {
  const cols = [
    { colKey: 'applicant', title: t('organization.joinRequests.columns.applicant'), ellipsis: true, minWidth: 160 },
    { colKey: 'request_type', title: t('organization.joinRequests.columns.type'), width: 88 },
    { colKey: 'requested_role', title: t('organization.joinRequests.columns.requestedRole'), width: 140 },
    { colKey: 'message', title: t('organization.joinRequests.columns.message'), ellipsis: true, minWidth: 120 },
    { colKey: 'created_at', title: t('organization.joinRequests.columns.appliedAt'), width: 154 },
    { colKey: 'actions', title: t('organization.members.columns.operations'), width: 88, align: 'left' },
  ]
  return cols
})

const sharedKbColumns = computed(() => {
  const cols = [
    { colKey: 'name', title: t('organization.sharedResources.columns.name'), ellipsis: true, minWidth: 180 },
    { colKey: 'shared_by', title: t('organization.sharedResources.columns.sharedBy'), width: 120, ellipsis: true },
    { colKey: 'created_at', title: t('organization.sharedResources.columns.sharedAt'), width: 154 },
    { colKey: 'space_permission', title: t('organization.settings.sharePermissionLabel'), width: 108 },
    { colKey: 'my_permission', title: t('organization.settings.myPermissionLabel'), width: 96 },
    {
      colKey: 'actions',
      title: t('organization.members.columns.operations'),
      width: isAdmin.value ? 96 : 64,
      align: 'left',
    },
  ]
  return cols
})

const sharedAgentColumns = computed(() => {
  const cols = [
    { colKey: 'name', title: t('organization.sharedResources.columns.name'), ellipsis: true, minWidth: 160 },
    { colKey: 'shared_by', title: t('organization.sharedResources.columns.sharedBy'), width: 108, ellipsis: true },
    { colKey: 'created_at', title: t('organization.sharedResources.columns.sharedAt'), width: 118 },
    { colKey: 'scope_kb', title: t('agent.shareScope.knowledgeBase'), width: 120, ellipsis: true },
    { colKey: 'scope_web_search', title: t('agent.shareScope.webSearch'), width: 88, ellipsis: true },
    { colKey: 'scope_mcp', title: t('agent.shareScope.mcp'), width: 108, ellipsis: true },
    { colKey: 'permission', title: t('organization.sharedResources.columns.permission'), width: 80 },
  ]
  if (isAdmin.value) {
    cols.push({ colKey: 'actions', title: t('organization.members.columns.operations'), width: 72, align: 'left' } as typeof cols[number])
  }
  return cols
})

function agentKbScopeLabel(share: AgentShareResponse): string {
  if (share.scope_kb === undefined || share.scope_kb === '') return '—'
  if (share.scope_kb === 'all') return t('agent.shareScope.kbAll')
  if (share.scope_kb === 'selected' && (share.scope_kb_count ?? 0) > 0) {
    return t('agent.shareScope.kbSelected', { count: share.scope_kb_count })
  }
  return t('agent.shareScope.kbNone')
}

function agentWebSearchScopeLabel(share: AgentShareResponse): string {
  if (share.scope_web_search === undefined) return '—'
  return share.scope_web_search ? t('agent.shareScope.enabled') : t('agent.shareScope.disabled')
}

function agentMcpScopeLabel(share: AgentShareResponse): string {
  if (share.scope_mcp === undefined || share.scope_mcp === '') return '—'
  if (share.scope_mcp === 'all') return t('agent.shareScope.mcpAll')
  if (share.scope_mcp === 'selected' && (share.scope_mcp_count ?? 0) > 0) {
    return t('agent.shareScope.mcpSelected', { count: share.scope_mcp_count })
  }
  return t('agent.shareScope.mcpNone')
}

const filteredMembers = computed(() => {
  const query = memberSearchQuery.value.toLowerCase()
  if (!query) return members.value
  return members.value.filter((m) =>
    (m.tenant_name || '').toLowerCase().includes(query) ||
    (m.username || '').toLowerCase().includes(query) ||
    (m.email || '').toLowerCase().includes(query)
  )
})

const filteredJoinRequests = computed(() => {
  const query = joinRequestSearchQuery.value.trim().toLowerCase()
  if (!query) return joinRequests.value
  return joinRequests.value.filter((req) => {
    const haystack = [req.username, req.email, req.user_id, req.message]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return haystack.includes(query)
  })
})

function joinRequestApplicantLabel(req: JoinRequestResponse): string {
  return req.username || req.email || req.user_id
}

function joinRequestApplicantSecondary(req: JoinRequestResponse): string {
  const primary = joinRequestApplicantLabel(req)
  if (req.email && req.email !== primary) return req.email
  return ''
}

// 成员行的主标题：优先展示「空间名」，回退到代表用户名 / 空间 ID。Plan 3
// 之后每一行成员都对应一个空间，UI 必须先于代表用户呈现空间身份，
// 否则用户会误以为这是按"人"加进来的。
const memberPrimaryLabel = (m: OrganizationMember): string => {
  return m.tenant_name || m.username || `tenant#${m.tenant_id}`
}

// 副标题：主标题展示的是空间名时，副标题展示代表用户名；如果主标题已经
// 是用户名（无 tenant_name 时的回退），副标题留空，避免重复信息。
// 邮箱在空间成员列表里没什么用（不是邀请人需要联系的对象），不展示。
const memberSecondaryLabel = (m: OrganizationMember): string => {
  if (m.tenant_name && m.username) {
    return m.username
  }
  return ''
}

// Owner identification is tenant-keyed after Plan 3 (#1303): the org's
// pinned owner_tenant_id (migration 000046) is the authority on which
// row in the per-tenant members list represents the owner. Falling
// back to owner_id (user-id) only matters for legacy rows where
// owner_tenant_id wasn't backfilled — in that case the old per-user
// rule is still better than nothing.
const isOwnerMember = (member: OrganizationMember): boolean => {
  const ownerTenantID = orgInfo.value?.owner_tenant_id
  if (ownerTenantID && ownerTenantID > 0) {
    return member.tenant_id === ownerTenantID
  }
  return member.user_id === orgInfo.value?.owner_id
}

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
      orgStore.setCurrentOrganization(res.data)
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
    await orgStore.fetchMembers(props.orgId)
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
const approvePopupRequestId = ref<string | null>(null)
const approveAssignRole = ref<'viewer' | 'editor' | 'admin'>('viewer')

function normalizeJoinRequestRole(role: string): 'viewer' | 'editor' | 'admin' {
  if (role === 'admin' || role === 'editor' || role === 'viewer') return role
  return 'viewer'
}

function openApprovePopup(req: JoinRequestResponse) {
  approvePopupRequestId.value = req.id
  approveAssignRole.value = normalizeJoinRequestRole(req.requested_role)
}

function closeApprovePopup() {
  approvePopupRequestId.value = null
}

function handleApprovePopupVisibleChange(visible: boolean, req: JoinRequestResponse) {
  if (visible) {
    openApprovePopup(req)
    return
  }
  if (approvePopupRequestId.value === req.id) {
    closeApprovePopup()
  }
}

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

/**
 * 审批结果会同时影响设置弹窗、空间卡片和全局侧栏中的待审批数量。
 * 后两处读取的是 organization store，因此必须绕过列表缓存并同步最新计数。
 */
const refreshOrganizationAfterReview = async () => {
  await Promise.all([
    fetchOrgDetail(),
    fetchMembers()
  ])
}

const confirmApproveRequest = async (req: JoinRequestResponse) => {
  const success = await handleApproveRequest(req, approveAssignRole.value)
  if (success) closeApprovePopup()
}

const handleApproveRequest = async (req: JoinRequestResponse, assignRole: 'viewer' | 'editor' | 'admin'): Promise<boolean> => {
  if (!props.orgId) return false
  reviewingRequestId.value = req.id
  try {
    const res = await orgStore.reviewOrganizationJoinRequest(
      props.orgId,
      req.id,
      { approved: true, role: assignRole }
    )
    if (res.success) {
      MessagePlugin.success(t('organization.settings.approveSuccess'))
      joinRequests.value = joinRequests.value.filter(r => r.id !== req.id)
      await refreshOrganizationAfterReview()
      return true
    }
    MessagePlugin.error(res.message || t('organization.settings.reviewFailed'))
    return false
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.settings.reviewFailed'))
    return false
  } finally {
    reviewingRequestId.value = null
  }
}

const handleRejectRequest = async (req: JoinRequestResponse) => {
  if (!props.orgId) return
  reviewingRequestId.value = req.id
  try {
    const res = await orgStore.reviewOrganizationJoinRequest(
      props.orgId,
      req.id,
      { approved: false }
    )
    if (res.success) {
      MessagePlugin.success(t('organization.settings.rejectSuccess'))
      joinRequests.value = joinRequests.value.filter(r => r.id !== req.id)
      await refreshOrganizationAfterReview()
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
      const result = await orgStore.updateOrganization(props.orgId, {
        name: formData.value.name.trim(),
        description: formData.value.description.trim(),
        avatar: formData.value.avatar || undefined,
        require_approval: formData.value.require_approval,
        searchable: formData.value.searchable,
        invite_code_validity_days: formData.value.invite_code_validity_days,
        member_limit: formData.value.member_limit
      })
      if (result) {
        MessagePlugin.success(t('common.saveSuccess'))
        emit('saved')
        handleClose()
      } else {
        MessagePlugin.error(orgStore.error || t('common.saveFailed'))
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
    const success = await orgStore.changeMemberRole(
      props.orgId,
      member.tenant_id,
      newRole as 'admin' | 'editor' | 'viewer'
    )
    if (success) {
      MessagePlugin.success(t('organization.roleUpdated'))
    } else {
      MessagePlugin.error(orgStore.error || t('organization.roleUpdateFailed'))
      fetchMembers()
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.roleUpdateFailed'))
    fetchMembers()
  }
}

const confirmRemoveMember = async (member: OrganizationMember) => {
  if (!props.orgId) return

  try {
    const success = await orgStore.kickMember(props.orgId, member.tenant_id)
    if (success) {
      MessagePlugin.success(t('organization.memberRemoved'))
    } else {
      MessagePlugin.error(orgStore.error || t('organization.memberRemoveFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.memberRemoveFailed'))
  }
}

watch(upgradePopupVisible, (visible) => {
  if (visible && upgradeRoleOptions.value.length > 0) {
    upgradeForm.value.requested_role = upgradeRoleOptions.value[0].value as 'editor' | 'admin'
  } else if (!visible && !upgradeSubmitting.value) {
    upgradeForm.value = { requested_role: 'editor', message: '' }
  }
})

const handleSubmitUpgrade = async () => {
  if (!props.orgId) return

  upgradeSubmitting.value = true
  try {
    const res = await orgStore.requestOrganizationRoleUpgrade(props.orgId, {
      requested_role: upgradeForm.value.requested_role,
      message: upgradeForm.value.message
    })
    if (res.success) {
      MessagePlugin.success(t('organization.upgrade.submitSuccess'))
      upgradePopupVisible.value = false
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

// 添加成员：搜索空间（按空间名 / 用户名 / 邮箱模糊匹配，按 tenant_id 去重）
let tenantSearchTimer: ReturnType<typeof setTimeout> | null = null
const handleTenantSearch = (query: string) => {
  if (tenantSearchTimer) {
    clearTimeout(tenantSearchTimer)
  }
  if (!query || query.length < 2) {
    tenantSearchResults.value = []
    return
  }
  tenantSearchTimer = setTimeout(async () => {
    if (!props.orgId) return
    tenantSearchLoading.value = true
    try {
      const res = await searchTenantsForInvite(props.orgId, query, 10)
      if (res.success && res.data) {
        tenantSearchResults.value = res.data
      }
    } catch (error) {
      console.error('Failed to search tenants:', error)
    } finally {
      tenantSearchLoading.value = false
    }
  }, 300)
}

// 添加成员：把选中的空间拉入空间。后端要求 tenant_id；representative_user_id
// 仅做展示/审计用，所以把搜索结果中代表用户也一并带上。
const handleAddMember = async () => {
  if (!props.orgId || selectedTenantId.value == null) return

  const candidate = tenantSearchResults.value.find(
    (c) => c.tenant_id === selectedTenantId.value
  )

  addMemberSubmitting.value = true
  try {
    const res = await orgStore.inviteOrganizationMember(props.orgId, {
      tenant_id: selectedTenantId.value,
      representative_user_id: candidate?.representative_user_id,
      role: addMemberRole.value,
    })
    if (res.success) {
      MessagePlugin.success(t('organization.addMember.success'))
      addMemberPopupVisible.value = false
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
  selectedTenantId.value = null
  addMemberRole.value = 'viewer'
  tenantSearchResults.value = []
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
    const code = await orgStore.refreshInviteCode(props.orgId)
    if (code) {
      inviteCode.value = code
      MessagePlugin.success(t('organization.inviteCodeRefreshed'))
      await fetchOrgDetail()
    } else {
      MessagePlugin.error(orgStore.error || t('organization.inviteCodeRefreshFailed'))
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
    const result = await orgStore.updateOrganization(props.orgId, {
      invite_code_validity_days: value
    })
    if (result) {
      MessagePlugin.success(t('common.saveSuccess'))
    } else {
      formData.value.invite_code_validity_days = orgInfo.value?.invite_code_validity_days ?? 7
      MessagePlugin.error(orgStore.error || t('common.saveFailed'))
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
    const result = await orgStore.updateOrganization(props.orgId, {
      require_approval: value
    })
    if (result) {
      MessagePlugin.success(t('common.saveSuccess'))
    } else {
      // 回滚
      formData.value.require_approval = !value
      MessagePlugin.error(orgStore.error || t('common.saveFailed'))
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
    const result = await orgStore.updateOrganization(props.orgId, {
      searchable: value
    })
    if (result) {
      MessagePlugin.success(t('common.saveSuccess'))
    } else {
      formData.value.searchable = !value
      MessagePlugin.error(orgStore.error || t('common.saveFailed'))
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
    const res = await orgStore.unshareKnowledgeBase(
      share.knowledge_base_id,
      share.id,
      props.orgId
    )
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
    const res = await orgStore.unshareAgent(share.agent_id, share.id, props.orgId)
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
    joinRequestSearchQuery.value = ''
    approvePopupRequestId.value = null
    joinRequests.value = []
    if (props.mode === 'create') {
      // 创建模式：重置表单
      formData.value = { name: '', description: '', avatar: '', require_approval: false, searchable: false, invite_code_validity_days: 7, member_limit: 50 }
      orgStore.clearCurrentOrganizationContext()
      sharedKnowledgeBases.value = []
      inviteCode.value = ''
      inviteCodeExpiresAt.value = null
    } else if (props.orgId) {
      fetchOrgDetail()
      fetchMembers()
      fetchSharedKBs()
    }
  }
})

watch(currentSection, (section) => {
  if (section === 'joinRequests' && props.orgId) {
    fetchJoinRequests()
  }
})

watch(addMemberPopupVisible, (visible) => {
  if (!visible) {
    resetAddMemberDialog()
  }
})
</script>

<style scoped lang="less">
@primary-color: var(--td-brand-color);
@primary-light: var(--td-brand-color-light);
@primary-lighter: var(--td-component-stroke);
@primary-hover: var(--td-brand-color-active);

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
  background: var(--td-bg-color-container);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.close-btn {
  position: absolute;
  top: 16px;
  right: 16px;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  transition: all 0.2s ease;
  z-index: 10;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.settings-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.settings-sidebar {
  width: 208px;
  background-color: var(--td-bg-color-settings-modal);
  border-right: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.sidebar-header {
  padding: 16px 14px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.sidebar-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.settings-nav {
  flex: 1;
  padding: 8px 8px 12px;
  overflow-y: auto;
  min-height: 0;
}

.nav-group-title {
  padding: 6px 14px 2px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;

  .settings-nav > &:first-child {
    padding-top: 2px;
  }

  .settings-nav > &:not(:first-child) {
    padding-top: 8px;
  }
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 6px 12px;
  margin-bottom: 2px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  font-size: 14px;
  color: var(--td-text-color-primary);
  user-select: none;

  &:hover {
    background-color: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.active {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.nav-icon {
  margin-right: 9px;
  font-size: 16px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: inherit;

  &.nav-icon-img {
    width: 16px;
    height: 16px;
  }
}

.nav-label {
  flex: 1;
}

.nav-badge {
  flex-shrink: 0;
  margin-left: 2px;
  padding: 0 6px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 16px;
  font-weight: 500;
  text-align: center;

  &.nav-badge-count {
    min-width: 20px;
  }
}

.settings-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background-color: var(--td-bg-color-container);
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  padding: 28px 40px 48px;
  box-sizing: border-box;
  scroll-padding-bottom: 24px;
}

.tenant-role-hint {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 16px;
  padding: 10px 12px;
  background: var(--td-warning-color-light);
  border: 1px solid var(--td-warning-color-focus);
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-warning-color-active);

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}

.section {
  width: 100%;
  animation: sectionFadeIn 0.25s ease;

  .section-header {
    margin-bottom: 20px;
    width: 100%;
    min-width: 0;

    h2 {
      margin: 0;
      font-family: var(--app-font-family);
      font-size: 20px;
      font-weight: 600;
      color: var(--td-text-color-primary);
    }

    .section-header-titlewrap h2 {
      line-height: 1.25;
    }

    .section-description {
      margin: 8px 0 0;
      font-family: var(--app-font-family);
      font-size: 14px;
      color: var(--td-text-color-secondary);
      line-height: 1.5;
    }

    .permission-calc-hint {
      margin-top: 6px;

      .hint-inner {
        display: inline-flex;
        align-items: center;
        gap: 6px;
        cursor: help;
        color: var(--td-text-color-secondary);
        font-size: 13px;
      }
    }
  }
}

@keyframes sectionFadeIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
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
  gap: 24px;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);
  min-width: 0;

  &:first-child {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
  }

  .setting-info {
    flex: 0 0 42%;
    max-width: 42%;
    min-width: 0;
    padding-right: 0;

    &.full-width {
      max-width: 100%;
      padding-right: 0;
    }

    label {
      display: block;
      font-size: 15px;
      font-weight: 500;
      color: var(--td-text-color-primary);
      margin-bottom: 4px;

      .required {
        color: var(--td-error-color);
        margin-left: 2px;
      }
    }

    .desc {
      font-size: 13px;
      color: var(--td-text-color-secondary);
      margin: 0;
      line-height: 1.5;
    }
  }

  .setting-control {
    flex: 1 1 58%;
    min-width: 0;
    max-width: 58%;
    display: flex;
    justify-content: flex-end;
    align-items: flex-start;
    overflow: hidden;

    &.full-width {
      max-width: 100%;
      justify-content: flex-start;
    }

    :deep(.t-select),
    :deep(.t-input),
    :deep(.t-textarea) {
      width: 100%;
      min-width: 0;
    }
  }

  &.setting-row-vertical {
    flex-direction: column;
    gap: 12px;

    .setting-info {
      max-width: 100%;
      padding-right: 0;
    }

    .setting-control {
      max-width: 100%;
      justify-content: flex-start;
    }
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
  background: var(--td-bg-color-container-hover);
}

.avatar-change-hint {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  line-height: 1.2;
}

.name-input-wrapper {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;

  .name-input {
    flex: 1;
    min-width: 0;
  }
}

// 创建模式权限说明卡片
.permissions-info {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.permission-card {
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
}

.permission-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.permission-icon {
  width: 40px;
  height: 40px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-anti);

  &.admin {
    background: linear-gradient(135deg, var(--td-brand-color), var(--td-brand-color-active));
  }

  &.editor {
    background: linear-gradient(135deg, var(--td-warning-color), var(--td-warning-color-active));
  }

  &.viewer {
    background: var(--td-bg-color-component-disabled);
  }
}

.permission-title {
  display: flex;
  align-items: center;
  gap: 8px;

  .role-name {
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
}

.permission-list {
  margin: 0;
  padding: 0;
  list-style: none;

  li {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 0;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .check-icon {
    color: var(--td-brand-color);
    font-size: 14px;
  }

  .close-icon {
    color: var(--td-error-color);
    font-size: 14px;
  }
}

.info-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 20px;
  padding: 12px 16px;
  background: var(--td-brand-color-light);
  border-radius: 8px;
  color: var(--td-brand-color);
  font-size: 13px;
  line-height: 20px;

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}

/* 头像 Emoji 弹层内容 */
.avatar-popover-content {
  padding: 12px;
  min-width: 260px;
}

.avatar-popover-title {
  margin: 0 0 10px 0;
  font-size: 12px;
  color: var(--td-text-color-secondary);
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
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  font-size: 18px;
  cursor: pointer;
  transition: border-color 0.2s ease, background 0.2s ease;
}

.avatar-popover-content .avatar-emoji-btn:hover {
  border-color: var(--td-brand-color);
  background: rgba(7, 192, 95, 0.06);
}

.avatar-popover-content .avatar-emoji-btn.is-selected {
  border-color: var(--td-brand-color);
  background: rgba(7, 192, 95, 0.12);
}

.avatar-popover-content .avatar-clear-btn {
  margin-top: 10px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.avatar-popover-content .avatar-clear-btn:hover {
  color: var(--td-brand-color-active);
}

// 邀请卡片样式
.invite-card {
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
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
        color: var(--td-text-color-primary);
      }
    }
  }

  .invite-code-box {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    padding: 10px 14px;

    .invite-code-value {
      font-family: var(--app-font-family-mono);
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
    color: var(--td-text-color-secondary);
  }

  .invite-validity-desc {
    font-size: 12px;
    color: var(--td-text-color-secondary);
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
      color: var(--td-text-color-secondary);
    }
  }

  .invite-divider {
    height: 1px;
    background: var(--td-bg-color-secondarycontainer);
    margin: 12px 0;
  }

  .invite-link-box {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    padding: 10px 14px;
    gap: 12px;

    .invite-link-value {
      flex: 1;
      font-size: 12px;
      color: var(--td-text-color-secondary);
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
      color: var(--td-text-color-placeholder);
    }
  }
}

// 成员管理（对齐 TenantMembers 列表 + 权限弹层）
.section-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  width: 100%;
  min-width: 0;
}

.section-header-titlewrap {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 1 1 auto;

  h2 {
    margin: 0;
    line-height: 1.25;
    white-space: nowrap;
  }
}

.permissions-trigger-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  margin: 0;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  line-height: 0;
  transition: background-color 0.2s ease, color 0.2s ease;

  :deep(.t-icon) {
    display: block;
  }

  &:hover {
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}

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

.members-list-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: 0 1 auto;
  min-width: 0;
}

.members-list-search {
  flex: 0 0 14rem;
  width: 14rem;
  min-width: 0;

  :deep(.t-input) {
    width: 100%;
  }
}

.members-list-add-btn {
  flex-shrink: 0;
}

.members-list-upgrade-btn {
  flex-shrink: 0;
}

.loading-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px 0 8px;
}

.empty-state {
  padding: 8px 0 16px;
}

.empty-state-title {
  margin: 0 0 4px;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.empty-state-desc {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.permission-hint-popover {
  padding: 14px 16px;
  max-width: 360px;

  .permission-hint-title {
    margin: 0 0 6px;
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .permission-hint-desc {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.shared-resources-wrap {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.resource-row-actions {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.resource-name {
  display: block;
  font-weight: 500;
  font-size: 14px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.resource-meta {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.member-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: 2px 0;

  .member-name {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-weight: 500;
    font-size: 14px;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 100%;
  }

  .member-email {
    font-size: 12px;
    line-height: 1.35;
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .me-tag,
  .owner-tag {
    display: inline-flex;
    align-items: center;
    padding: 0 5px;
    height: 16px;
    border-radius: 3px;
    font-size: 10px;
    font-weight: 500;
    flex-shrink: 0;
  }

  .me-tag {
    background: @primary-color;
    color: var(--td-text-color-anti);
  }

  .owner-tag {
    background: var(--td-brand-color-light);
    color: @primary-color;
  }
}

.data-table-shell {
  overflow-x: auto;
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke);
  background-color: var(--td-bg-color-container);

  :deep(thead th) {
    font-weight: 600;
    font-size: 13px;
  }

  :deep(.t-table td),
  :deep(.t-table th) {
    padding-top: 12px;
    padding-bottom: 12px;
  }

  :deep(.role-cell) {
    display: flex;
    align-items: center;
    min-width: 0;
    box-sizing: border-box;
  }

  :deep(.member-role-select.t-select) {
    width: 100%;
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

@media (max-width: 560px) {
  .members-list-actions {
    width: 100%;
    justify-content: flex-start;
  }

  .members-list-search {
    flex: 1 1 auto;
    width: auto;
    max-width: none;
  }
}

// Join requests table
.join-requests-wrap {
  .join-request-message {
    display: block;
    max-width: 220px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .join-request-role-change {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 13px;
    color: var(--td-text-color-secondary);

    .t-icon {
      flex-shrink: 0;
      color: var(--td-text-color-placeholder);
    }
  }

  .join-request-actions {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }
}

.settings-footer {
  padding: 12px 40px;
  border-top: 1px solid var(--td-component-stroke);
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  background-color: var(--td-bg-color-container);
}

.settings-nav::-webkit-scrollbar,
.content-wrapper::-webkit-scrollbar {
  width: 6px;
}

.settings-nav::-webkit-scrollbar-track {
  background: var(--td-bg-color-secondarycontainer);
}

.settings-nav::-webkit-scrollbar-thumb {
  background: var(--td-gray-color-5);
  border-radius: 3px;
}

.settings-nav::-webkit-scrollbar-thumb:hover {
  background: var(--td-gray-color-6);
}

.content-wrapper::-webkit-scrollbar-track {
  background: var(--td-bg-color-container);
}

.content-wrapper::-webkit-scrollbar-thumb {
  background: var(--td-gray-color-5);
  border-radius: 3px;
}

.content-wrapper::-webkit-scrollbar-thumb:hover {
  background: var(--td-gray-color-6);
}

// Transitions
.modal-enter-active,
.modal-leave-active {
  transition: all 0.3s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.95);
  }
}

// 权限升级申请弹出层（对齐添加成员 popup）

.add-member-tip {
  margin: 0 0 14px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.member-invite-popup-inner {
  width: min(400px, calc(100vw - 32px));
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
  :deep(.t-form__item) {
    margin-bottom: 14px;

    &:last-child {
      margin-bottom: 4px;
    }
  }

  :deep(.t-form__label) {
    font-weight: 500;
    padding-bottom: 6px;
  }

  :deep(.t-select) {
    width: 100%;
  }

  :deep(.t-textarea) {
    width: 100%;
  }

  .member-form-control {
    width: 100%;
    min-width: 0;
  }

  .field-hint {
    margin: 6px 0 0;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    line-height: 1.45;
  }
}

.invite-popup-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--td-component-stroke);
}
</style>

<style lang="less">
/* 权限说明 / 提示弹出层（t-popup 挂到 body，须全局样式） */
.org-permissions-popup-overlay {
  z-index: 3050 !important;

  .t-popup__content {
    padding: 0 !important;
    border-radius: 12px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 4px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.1) !important;
    backdrop-filter: blur(20px) saturate(180%) !important;
    -webkit-backdrop-filter: blur(20px) saturate(180%) !important;
    overflow: hidden;
  }

  .permission-hint-popover {
    padding: 14px 16px;

    .permission-hint-title {
      margin: 0 0 6px;
      font-size: 14px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      line-height: 1.35;
    }

    .permission-hint-desc {
      margin: 0;
      font-size: 13px;
      line-height: 1.55;
      color: var(--td-text-color-secondary);
    }
  }

  .permissions-compact.permissions-compact--popover {
    padding: 12px 14px;
    margin: 0;
    max-height: min(392px, calc(65vh - 8px));
    overflow-x: hidden;
    overflow-y: auto;

    .permissions-compact-header {
      display: flex;
      flex-direction: column;
      gap: 2px;
      margin-bottom: 10px;

      .permissions-compact-title {
        font-size: 13px;
        font-weight: 600;
        color: var(--td-text-color-primary);
      }

      .permissions-compact-desc {
        font-size: 12px;
        line-height: 1.45;
        color: var(--td-text-color-secondary);
      }
    }

    .permissions-compact-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
    }

    .perm-role-block {
      border: 1px solid var(--td-component-stroke);
      border-radius: 6px;
      padding: 8px 10px;
      background: var(--td-bg-color-container);

      &.is-me {
        border-color: var(--td-brand-color);
        background: var(--td-brand-color-light);
      }

      .perm-role-tag {
        display: flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        font-weight: 600;
        color: var(--td-text-color-primary);
        margin-bottom: 6px;

        .me-badge {
          margin-left: auto;
          font-size: 10px;
          font-weight: 500;
          color: var(--td-brand-color);
          padding: 1px 5px;
          background: var(--td-brand-color-light);
          border-radius: 4px;
        }
      }

      .perm-items {
        display: flex;
        flex-direction: column;
        gap: 3px;

        .perm-item {
          display: flex;
          align-items: flex-start;
          gap: 4px;
          font-size: 11px;
          line-height: 1.35;
          color: var(--td-text-color-secondary);

          .t-icon {
            margin-top: 1px;
            flex-shrink: 0;
          }

          &.has .t-icon {
            color: var(--td-brand-color);
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
  }
}

:root[theme-mode='dark'] .org-permissions-popup-overlay .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 4px rgba(0, 0, 0, 0.12),
    0 8px 32px rgba(0, 0, 0, 0.28) !important;
}

@media (max-width: 480px) {
  .org-permissions-popup-overlay .permissions-compact.permissions-compact--popover .permissions-compact-grid {
    grid-template-columns: 1fr;
  }
}

/* 添加成员 / 权限升级弹出层（与空间成员管理邀请弹层一致） */
.org-add-member-popup-overlay,
.org-upgrade-popup-overlay,
.org-approve-request-popup-overlay {
  z-index: 3050 !important;

  .t-popup__content {
    padding: 16px;
    border-radius: 10px;
    border: 1px solid var(--td-component-stroke);
    box-shadow: var(--td-shadow-2), 0 8px 24px rgba(15, 23, 42, 0.08);
  }
}

.org-upgrade-popup-overlay,
.org-approve-request-popup-overlay {
  .org-upgrade-popup-inner,
  .org-approve-request-popup-inner {
    width: min(360px, calc(100vw - 32px));
    max-width: 100%;
    box-sizing: border-box;
  }

  .member-invite-popup-title {
    margin: 0 0 10px;
    font-size: 15px;
    font-weight: 600;
    line-height: 1.35;
    color: var(--td-text-color-primary);
  }

  .add-member-tip {
    margin: 0 0 12px;
    padding: 10px 12px;
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    font-size: 13px;
    color: var(--td-text-color-secondary);
    line-height: 1.5;
  }

  .upgrade-current-role-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 14px;
    padding: 10px 12px;
    border-radius: 8px;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
  }

  .upgrade-current-role-label {
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .org-upgrade-fields {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .org-upgrade-field {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
    min-width: 0;
  }

  .org-upgrade-field-label {
    display: block;
    margin: 0;
    font-size: 14px;
    font-weight: 500;
    line-height: 1.4;
    color: var(--td-text-color-primary);
  }

  .upgrade-role-pills {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    width: 100%;
  }

  .upgrade-role-pill {
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

  .org-upgrade-field .t-textarea,
  .org-upgrade-field .t-select {
    width: 100%;
    box-sizing: border-box;
  }

  .invite-popup-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
    padding-top: 12px;
    border-top: 1px solid var(--td-component-stroke);
  }
}
</style>
