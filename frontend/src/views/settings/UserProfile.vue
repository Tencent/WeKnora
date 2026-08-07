<template>
  <div class="user-profile">
    <div class="section-header">
      <h2>{{ $t('userProfile.title') }}</h2>
      <p class="section-description">{{ $t('userProfile.description') }}</p>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('tenant.loadingInfo') }}</span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadInfo">{{ $t('tenant.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <!-- Content -->
    <div v-else class="settings-group">
      <!-- 用户 ID -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.userIdLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.userIdDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ userInfo?.id || '-' }}</span>
        </div>
      </div>

      <!-- 用户名 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.usernameLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.usernameDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ userInfo?.username || '-' }}</span>
        </div>
      </div>

      <!-- 邮箱 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.emailLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.emailDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ userInfo?.email || '-' }}</span>
        </div>
      </div>

      <!-- 注册时间 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('tenant.api.createdAtLabel') }}</label>
          <p class="desc">{{ $t('tenant.api.createdAtDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ formatDate(userInfo?.created_at) }}</span>
        </div>
      </div>

      <!-- 修改密码 -->
      <div class="setting-row password-row">
        <div class="setting-info">
          <label>{{ $t('userProfile.changePassword.label') }}</label>
          <p class="desc">
            {{ oidcOnlyLogin
              ? $t('userProfile.changePassword.oidcOnlyDescription')
              : $t('userProfile.changePassword.description') }}
          </p>
        </div>
        <div class="setting-control password-control">
          <t-alert
            v-if="oidcOnlyLogin"
            theme="info"
            :message="$t('userProfile.changePassword.oidcOnlyNotice')"
            class="oidc-only-notice"
          />
          <t-form
            v-else
            ref="passwordFormRef"
            :data="passwordForm"
            :rules="passwordRules"
            label-align="top"
            class="password-form"
            @submit.prevent
          >
            <t-form-item :label="$t('userProfile.changePassword.currentLabel')" name="oldPassword">
              <t-input
                v-model="passwordForm.oldPassword"
                type="password"
                autocomplete="current-password"
                :disabled="passwordSubmitting"
                :placeholder="$t('userProfile.changePassword.currentPlaceholder')"
              >
                <template #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>
            </t-form-item>
            <t-form-item :label="$t('userProfile.changePassword.newLabel')" name="newPassword">
              <t-input
                v-model="passwordForm.newPassword"
                type="password"
                autocomplete="new-password"
                :disabled="passwordSubmitting"
                :placeholder="$t('userProfile.changePassword.newPlaceholder')"
              >
                <template #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>
            </t-form-item>
            <t-form-item :label="$t('userProfile.changePassword.confirmLabel')" name="confirmPassword">
              <t-input
                v-model="passwordForm.confirmPassword"
                type="password"
                autocomplete="new-password"
                :disabled="passwordSubmitting"
                :placeholder="$t('userProfile.changePassword.confirmPlaceholder')"
                @enter="submitPasswordChange"
              >
                <template #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>
            </t-form-item>
            <div class="password-actions">
              <t-button
                theme="primary"
                :loading="passwordSubmitting"
                :disabled="passwordSubmitting"
                @click="submitPasswordChange"
              >
                {{ $t('userProfile.changePassword.submit') }}
              </t-button>
            </div>
          </t-form>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import type { FormInstanceFunctions, FormRule } from 'tdesign-vue-next'
import {
  getCurrentUser,
  changePassword,
  logout as logoutApi,
  type UserInfo,
} from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

const userInfo = ref<UserInfo | null>(null)
const loading = ref(true)
const error = ref('')

const passwordFormRef = ref<FormInstanceFunctions | null>(null)
const passwordSubmitting = ref(false)
const passwordForm = reactive({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})

const oidcOnlyLogin = computed(
  () => userInfo.value?.preferences?.oidc_only_login === true,
)

const passwordRules = computed<Record<string, FormRule[]>>(() => ({
  oldPassword: [
    { required: true, message: t('userProfile.changePassword.currentRequired'), type: 'error' },
  ],
  newPassword: [
    { required: true, message: t('auth.passwordRequired'), type: 'error' },
    { min: 8, message: t('auth.passwordMinLength'), type: 'error' },
    { max: 32, message: t('auth.passwordMaxLength'), type: 'error' },
    { pattern: /[a-zA-Z]/, message: t('auth.passwordMustContainLetter'), type: 'error' },
    { pattern: /\d/, message: t('auth.passwordMustContainNumber'), type: 'error' },
    {
      validator: (val: string) => val !== passwordForm.oldPassword,
      message: t('userProfile.changePassword.sameAsCurrent'),
      type: 'error',
    },
  ],
  confirmPassword: [
    { required: true, message: t('auth.confirmPasswordRequired'), type: 'error' },
    {
      validator: (val: string) => val === passwordForm.newPassword,
      message: t('auth.passwordMismatch'),
      type: 'error',
      trigger: 'blur',
    },
  ],
}))

const loadInfo = async () => {
  try {
    loading.value = true
    error.value = ''
    const resp = await getCurrentUser()
    if ((resp as any).success && resp.data) {
      userInfo.value = resp.data.user
    } else {
      error.value = resp.message || t('tenant.messages.fetchFailed')
    }
  } catch (err: any) {
    error.value = err?.message || t('tenant.messages.networkError')
  } finally {
    loading.value = false
  }
}

const formatDate = (dateStr: string | undefined) => {
  if (!dateStr) return t('tenant.unknown')
  try {
    const d = new Date(dateStr)
    const fmt = new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
    return fmt.format(d)
  } catch {
    return t('tenant.formatError')
  }
}

const resetPasswordForm = () => {
  passwordForm.oldPassword = ''
  passwordForm.newPassword = ''
  passwordForm.confirmPassword = ''
  passwordFormRef.value?.clearValidate?.()
}

const submitPasswordChange = async () => {
  if (passwordSubmitting.value) return
  const result = await passwordFormRef.value?.validate?.()
  if (result !== true) return

  passwordSubmitting.value = true
  try {
    const resp = await changePassword({
      old_password: passwordForm.oldPassword,
      new_password: passwordForm.newPassword,
    })
    if (!resp.success) {
      MessagePlugin.error(resp.message || t('userProfile.changePassword.failed'))
      return
    }

    MessagePlugin.success(t('userProfile.changePassword.success'))
    resetPasswordForm()

    // Backend revokes all sessions on success; mirror that locally and
    // force a fresh login with the new credential.
    try {
      await logoutApi()
    } catch {
      /* ignore — local cleanup still proceeds */
    }
    authStore.logout()
    router.push('/login')
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('userProfile.changePassword.failed'))
  } finally {
    passwordSubmitting.value = false
  }
}

onMounted(loadInfo)
</script>

<style lang="less" scoped>
.user-profile {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.loading-inline {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 40px 0;
  justify-content: center;
  color: var(--td-text-color-secondary);
  font-size: 14px;
}

.error-inline {
  padding: 20px 0;
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
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: flex-start;

  .info-value {
    font-size: 14px;
    color: var(--td-text-color-primary);
    text-align: right;
    word-break: break-word;
  }
}

.password-row {
  align-items: flex-start;
}

.password-control {
  min-width: 320px;
  max-width: 360px;
  flex-direction: column;
  align-items: stretch;
}

.oidc-only-notice {
  width: 100%;
}

.password-form {
  width: 100%;

  :deep(.t-form__item) {
    margin-bottom: 16px;
  }
}

.password-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 4px;
}
</style>
