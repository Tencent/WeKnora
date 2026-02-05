<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="handleClose">
        <div class="settings-modal" :class="{ 'join-mode': mode === 'join' }">
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
                  v-for="(item, index) in navItems"
                  :key="index"
                  :class="['nav-item', { 'active': currentSection === item.key }]"
                  @click="currentSection = item.key"
                >
                  <t-icon :name="item.icon" class="nav-icon" />
                  <span class="nav-label">{{ item.label }}</span>
                </div>
              </div>
            </div>

            <!-- 右侧内容区域 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <!-- 创建组织 - 基本信息 -->
                <div v-if="mode === 'create'" v-show="currentSection === 'basic'" class="section">
                  <div class="section-content">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('organization.editor.basicTitle') }}</h3>
                      <p class="section-desc">{{ $t('organization.editor.basicDesc') }}</p>
                    </div>
                    <div class="section-body">
                      <div class="form-item">
                        <label class="form-label required">{{ $t('organization.name') }}</label>
                        <div class="name-input-wrapper">
                          <SpaceAvatar :name="createForm.name || '?'" size="large" />
                          <t-input
                            v-model="createForm.name"
                            :placeholder="$t('organization.namePlaceholder')"
                            :maxlength="100"
                            class="name-input"
                          />
                        </div>
                        <p class="form-tip">{{ $t('organization.editor.nameTip') }}</p>
                      </div>
                      <div class="form-item">
                        <label class="form-label">{{ $t('organization.description') }}</label>
                        <t-textarea
                          v-model="createForm.description"
                          :placeholder="$t('organization.descriptionPlaceholder')"
                          :maxlength="500"
                          :autosize="{ minRows: 3, maxRows: 6 }"
                        />
                        <p class="form-tip">{{ $t('organization.editor.descriptionTip') }}</p>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 创建组织 - 权限说明 -->
                <div v-if="mode === 'create'" v-show="currentSection === 'permissions'" class="section">
                  <div class="section-content">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('organization.editor.permissionsTitle') }}</h3>
                      <p class="section-desc">{{ $t('organization.editor.permissionsDesc') }}</p>
                    </div>
                    <div class="section-body">
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
                  </div>
                </div>

                <!-- 加入组织 -->
                <div v-if="mode === 'join'" v-show="currentSection === 'join'" class="section">
                  <div class="section-content">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('organization.editor.joinTitle') }}</h3>
                      <p class="section-desc">{{ $t('organization.editor.joinDesc') }}</p>
                    </div>
                    <div class="section-body">
                      <div class="join-illustration">
                        <div class="illustration-icon">
                          <t-icon name="user-add" size="48px" />
                        </div>
                        <p class="illustration-text">{{ $t('organization.editor.joinIllustration') }}</p>
                      </div>
                      <div class="form-item">
                        <label class="form-label required">{{ $t('organization.inviteCode') }}</label>
                        <t-input
                          v-model="joinForm.invite_code"
                          :placeholder="$t('organization.inviteCodePlaceholder')"
                          :maxlength="32"
                          size="large"
                          class="invite-code-input"
                        />
                        <p class="form-tip">{{ $t('organization.editor.inviteCodeTip') }}</p>
                      </div>
                      <div class="join-steps">
                        <div class="step-title">{{ $t('organization.editor.howToGetCode') }}</div>
                        <div class="step-list">
                          <div class="step-item">
                            <span class="step-number">1</span>
                            <span class="step-text">{{ $t('organization.editor.step1') }}</span>
                          </div>
                          <div class="step-item">
                            <span class="step-number">2</span>
                            <span class="step-text">{{ $t('organization.editor.step2') }}</span>
                          </div>
                          <div class="step-item">
                            <span class="step-number">3</span>
                            <span class="step-text">{{ $t('organization.editor.step3') }}</span>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <!-- 底部按钮 -->
              <div class="settings-footer">
                <t-button theme="default" variant="outline" @click="handleClose">
                  {{ $t('common.cancel') }}
                </t-button>
                <t-button theme="primary" @click="handleSubmit" :loading="submitting">
                  {{ mode === 'create' ? $t('common.create') : $t('organization.join.preview') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>

    <!-- 加入确认弹窗 -->
    <t-dialog
      v-model:visible="showJoinConfirm"
      :header="$t('organization.join.confirmTitle')"
      :confirm-btn="previewInfo?.is_already_member ? $t('common.close') : $t('organization.join.confirm')"
      :cancel-btn="previewInfo?.is_already_member ? null : $t('common.cancel')"
      :confirm-on-enter="!previewInfo?.is_already_member"
      @confirm="previewInfo?.is_already_member ? (showJoinConfirm = false) : confirmJoin()"
      @cancel="showJoinConfirm = false"
      :confirm-loading="joining"
    >
      <div v-if="previewInfo" class="join-confirm-content">
        <div class="org-preview-card">
          <div class="org-preview-header">
            <div class="org-avatar">
              <t-icon name="usergroup" size="24px" />
            </div>
            <div class="org-info">
              <h4 class="org-name">{{ previewInfo.name }}</h4>
              <p class="org-desc">{{ previewInfo.description || $t('organization.noDescription') }}</p>
            </div>
          </div>
          <div class="org-stats">
            <div class="stat-item">
              <t-icon name="user" />
              <span>{{ $t('organization.join.memberCount', { count: previewInfo.member_count }) }}</span>
            </div>
            <div class="stat-item">
              <t-icon name="folder" />
              <span>{{ $t('organization.join.shareCount', { count: previewInfo.share_count }) }}</span>
            </div>
            <div class="stat-item stat-item-agent">
              <img src="@/assets/img/agent.svg" class="stat-agent-icon" alt="" aria-hidden="true" />
              <span>{{ $t('organization.join.agentShareCount', { count: previewInfo.agent_share_count ?? 0 }) }}</span>
            </div>
          </div>
        </div>
        <div v-if="previewInfo.is_already_member" class="already-member-notice">
          <t-icon name="check-circle-filled" />
          <span>{{ $t('organization.join.alreadyMember') }}</span>
        </div>
      </div>
    </t-dialog>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useOrganizationStore } from '@/stores/organization'
import { useI18n } from 'vue-i18n'
import type { OrganizationPreview } from '@/api/organization'
import SpaceAvatar from '@/components/SpaceAvatar.vue'

const { t } = useI18n()
const orgStore = useOrganizationStore()

// Props
const props = defineProps<{
  visible: boolean
  mode: 'create' | 'join'
}>()

// Emits
const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'success'): void
}>()

const currentSection = ref<string>('basic')
const submitting = ref(false)
const showJoinConfirm = ref(false)
const previewInfo = ref<OrganizationPreview | null>(null)
const joining = ref(false)

const createForm = ref({
  name: '',
  description: ''
})

const joinForm = ref({
  invite_code: ''
})

// 计算属性
const modalTitle = computed(() => {
  return props.mode === 'create' 
    ? t('organization.createOrg') 
    : t('organization.joinOrg')
})

const navItems = computed(() => {
  if (props.mode === 'create') {
    return [
      { key: 'basic', icon: 'info-circle', label: t('organization.editor.navBasic') },
      { key: 'permissions', icon: 'user-safety', label: t('organization.editor.navPermissions') }
    ]
  } else {
    return [
      { key: 'join', icon: 'user-add', label: t('organization.editor.navJoin') }
    ]
  }
})

// 方法
const resetForm = () => {
  createForm.value = { name: '', description: '' }
  joinForm.value = { invite_code: '' }
  currentSection.value = props.mode === 'create' ? 'basic' : 'join'
  showJoinConfirm.value = false
  previewInfo.value = null
}

const handleClose = () => {
  emit('update:visible', false)
  setTimeout(resetForm, 300)
}

const handleSubmit = async () => {
  if (props.mode === 'create') {
    await handleCreate()
  } else {
    await handleJoin()
  }
}

const handleCreate = async () => {
  if (!createForm.value.name.trim()) {
    MessagePlugin.warning(t('organization.nameRequired'))
    currentSection.value = 'basic'
    return
  }

  submitting.value = true
  try {
    const result = await orgStore.create(
      createForm.value.name.trim(),
      createForm.value.description.trim()
    )
    if (result) {
      MessagePlugin.success(t('organization.createSuccess'))
      emit('success')
      handleClose()
    } else {
      MessagePlugin.error(orgStore.error || t('organization.createFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.createFailed'))
  } finally {
    submitting.value = false
  }
}

const handleJoin = async () => {
  if (!joinForm.value.invite_code.trim()) {
    MessagePlugin.warning(t('organization.inviteCodeRequired'))
    return
  }

  submitting.value = true
  try {
    // First preview the organization
    const preview = await orgStore.preview(joinForm.value.invite_code.trim())
    if (preview) {
      previewInfo.value = preview
      showJoinConfirm.value = true
    } else {
      MessagePlugin.error(orgStore.error || t('organization.join.invalidCode'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.join.invalidCode'))
  } finally {
    submitting.value = false
  }
}

const confirmJoin = async () => {
  if (!joinForm.value.invite_code.trim()) {
    return
  }

  joining.value = true
  try {
    const result = await orgStore.join(joinForm.value.invite_code.trim())
    if (result) {
      MessagePlugin.success(t('organization.joinSuccess'))
      showJoinConfirm.value = false
      emit('success')
      handleClose()
    } else {
      MessagePlugin.error(orgStore.error || t('organization.joinFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('organization.joinFailed'))
  } finally {
    joining.value = false
  }
}

// 监听
watch(() => props.visible, (newVal) => {
  if (newVal) {
    resetForm()
  }
})

watch(() => props.mode, () => {
  currentSection.value = props.mode === 'create' ? 'basic' : 'join'
})
</script>

<style scoped lang="less">
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
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.settings-modal {
  position: relative;
  width: 90vw;
  max-width: 900px;
  height: 80vh;
  max-height: 650px;
  background: #fff;
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;

  &.join-mode {
    max-width: 700px;
    max-height: 580px;
  }
}

.close-btn {
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

.settings-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.settings-sidebar {
  width: 200px;
  background: #fafafa;
  border-right: 1px solid #e5e5e5;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}

.sidebar-header {
  padding: 24px 20px;
  border-bottom: 1px solid #e5e5e5;
}

.sidebar-title {
  margin: 0;
  font-family: "PingFang SC";
  font-size: 18px;
  font-weight: 600;
  color: #000000e6;
}

.settings-nav {
  flex: 1;
  padding: 12px 8px;
  overflow-y: auto;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 10px 12px;
  margin-bottom: 4px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  font-family: "PingFang SC";
  font-size: 14px;
  color: #00000099;

  &:hover {
    background: #f0f0f0;
  }

  &.active {
    background: #07c05f1a;
    color: #07c05f;
    font-weight: 500;
  }
}

.nav-icon {
  margin-right: 8px;
  font-size: 18px;
  flex-shrink: 0;
}

.nav-label {
  flex: 1;
}

.settings-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding: 24px 32px;
}

.section {
  margin-bottom: 32px;
}

.section-content {
  .section-header {
    margin-bottom: 24px;
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

.form-item {
  margin-bottom: 24px;

  &:last-child {
    margin-bottom: 0;
  }
}

.form-label {
  display: block;
  margin-bottom: 8px;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #000000e6;

  &.required::after {
    content: '*';
    color: #FA5151;
    margin-left: 4px;
  }
}

.form-tip {
  margin-top: 8px;
  font-size: 12px;
  color: #00000066;
  line-height: 18px;
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

// 权限说明样式
.permissions-info {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.permission-card {
  background: #fafafa;
  border-radius: 8px;
  padding: 16px;
  border: 1px solid #e5e5e5;
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
  color: #fff;

  &.admin {
    background: linear-gradient(135deg, #07c05f, #05a04f);
  }

  &.editor {
    background: linear-gradient(135deg, #f5a623, #e09612);
  }

  &.viewer {
    background: linear-gradient(135deg, #909399, #606266);
  }
}

.permission-title {
  display: flex;
  align-items: center;
  gap: 8px;

  .role-name {
    font-size: 15px;
    font-weight: 600;
    color: #000000e6;
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
    color: #00000099;
  }

  .check-icon {
    color: #07c05f;
    font-size: 14px;
  }

  .close-icon {
    color: #FA5151;
    font-size: 14px;
  }
}

.info-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 20px;
  padding: 12px 16px;
  background: #e8f4ff;
  border-radius: 8px;
  color: #0052d9;
  font-size: 13px;
  line-height: 20px;

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}

// 加入组织样式
.join-illustration {
  text-align: center;
  padding: 24px 0 32px;

  .illustration-icon {
    width: 80px;
    height: 80px;
    margin: 0 auto 16px;
    background: linear-gradient(135deg, #07c05f1a, #07c05f0d);
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #07c05f;
  }

  .illustration-text {
    margin: 0;
    font-size: 14px;
    color: #00000066;
  }
}

.invite-code-input {
  :deep(.t-input__inner) {
    font-size: 16px;
    letter-spacing: 1px;
    text-align: center;
  }
}

.join-steps {
  margin-top: 32px;
  padding: 20px;
  background: #fafafa;
  border-radius: 8px;

  .step-title {
    font-size: 14px;
    font-weight: 500;
    color: #000000e6;
    margin-bottom: 16px;
  }

  .step-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .step-item {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .step-number {
    width: 24px;
    height: 24px;
    background: #07c05f;
    color: #fff;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 600;
    flex-shrink: 0;
  }

  .step-text {
    font-size: 13px;
    color: #00000099;
  }
}

.settings-footer {
  padding: 16px 32px;
  border-top: 1px solid #e5e5e5;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
}

// 过渡动画
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

// 加入确认弹窗样式
.join-confirm-content {
  padding: 8px 0;
}

.org-preview-card {
  background: #fafafa;
  border-radius: 8px;
  padding: 16px;
  border: 1px solid #e5e5e5;
}

.org-preview-header {
  display: flex;
  gap: 12px;
  margin-bottom: 16px;
}

.org-avatar {
  width: 48px;
  height: 48px;
  background: linear-gradient(135deg, #07c05f, #05a04f);
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  flex-shrink: 0;
}

.org-info {
  flex: 1;
  min-width: 0;
}

.org-name {
  margin: 0 0 4px 0;
  font-size: 16px;
  font-weight: 600;
  color: #000000e6;
}

.org-desc {
  margin: 0;
  font-size: 13px;
  color: #00000066;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.org-stats {
  display: flex;
  gap: 24px;
  padding-top: 12px;
  border-top: 1px solid #e5e5e5;
}

.stat-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #00000099;

  .t-icon {
    font-size: 16px;
    color: #00000066;
  }

  &.stat-item-agent .stat-agent-icon {
    width: 16px;
    height: 16px;
    flex-shrink: 0;
  }
}

.already-member-notice {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 16px;
  padding: 12px 16px;
  background: #e8f4ff;
  border-radius: 8px;
  color: #0052d9;
  font-size: 14px;

  .t-icon {
    font-size: 18px;
    color: #07c05f;
  }
}
</style>
