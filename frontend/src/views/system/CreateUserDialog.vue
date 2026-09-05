<!--
  CreateUserDialog: SystemAdmin "Create user" dialog (Access settings row).

  Owns the whole create-user flow: form (username + email + auto-generate
  toggle revealing new/confirm password fields) and, when the server mints a
  one-time password, a reveal view that must be acknowledged before the
  dialog can close (overlay click, Esc, and the close button are disabled
  via `locked`). Esc is also captured on window so the parent Settings
  modal cannot unmount this dialog and discard the password. The idempotent
  retry path closes with its own success toast instead.

  Visibility is owned by the parent via v-model:visible. Every user-visible
  message is also emitted via `announced` so the parent's sr-only live
  region can relay it. The shared teleported-dialog shell comes from the
  systemAdminDialog.less import in SystemSettings.vue.
-->
<template>
  <t-dialog
    :visible="visible"
    :header="createUserSuccess
      ? t('system.globalSettings.createUser.generated.successTitle')
      : t('system.globalSettings.createUser.dialogTitle')"
    width="440px"
    placement="center"
    dialog-class-name="create-user-dialog"
    :footer="!createUserSuccess"
    :confirm-btn="{
      content: t('system.globalSettings.createUser.confirmBtn'),
      theme: 'primary',
      loading: submitting,
    }"
    :cancel-btn="{
      content: t('system.globalSettings.confirm.cancelBtn'),
      variant: 'outline',
    }"
    :close-on-overlay-click="!locked"
    :close-btn="!locked"
    :close-on-esc-keydown="!locked"
    @update:visible="onVisibleChange"
    @esc-keydown="onEscKeydown"
    @close="resetFormFields"
    @closed="onClosed"
    @confirm="submit"
  >
    <template v-if="createUserSuccess">
      <t-alert
        theme="success"
        :message="t('system.globalSettings.createUser.generated.successBody')"
        class="create-user-warning"
      />
      <div class="create-user-reveal">
        <div class="create-user-reveal-row">
          <span class="create-user-reveal-label">
            {{ t('system.globalSettings.createUser.generated.usernameLabel') }}
          </span>
          <span class="create-user-reveal-value">{{ createUserSuccess.username }}</span>
        </div>
        <div class="create-user-reveal-row">
          <span class="create-user-reveal-label">
            {{ t('system.globalSettings.createUser.generated.emailLabel') }}
          </span>
          <span class="create-user-reveal-value">{{ createUserSuccess.email }}</span>
        </div>
        <div class="create-user-reveal-row create-user-reveal-row--password">
          <span class="create-user-reveal-label">
            {{ t('system.globalSettings.createUser.generated.passwordLabel') }}
          </span>
          <div class="create-user-reveal-password">
            <pre class="create-user-reveal-password-value">{{ createUserSuccess.generatedPassword }}</pre>
            <t-button theme="primary" variant="outline" @click="copyGeneratedPassword">
              {{ t('system.globalSettings.createUser.generated.copyBtn') }}
            </t-button>
          </div>
        </div>
      </div>
      <div class="create-user-acknowledge">
        <t-button theme="primary" block @click="acknowledge">
          {{ t('system.globalSettings.createUser.generated.acknowledgeBtn') }}
        </t-button>
      </div>
    </template>
    <template v-else>
      <t-alert
        theme="warning"
        :message="t('system.globalSettings.createUser.warning')"
        class="create-user-warning"
      />
      <t-form
        ref="formRef"
        :data="form"
        :rules="rules"
        label-align="top"
        class="create-user-form"
      >
        <t-form-item :label="t('system.globalSettings.createUser.usernameLabel')" name="username">
          <t-input
            v-model="form.username"
            type="text"
            clearable
            autocomplete="off"
            :disabled="submitting"
            :placeholder="t('system.globalSettings.createUser.usernamePlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="t('system.globalSettings.createUser.emailLabel')" name="email">
          <t-input
            v-model="form.email"
            type="text"
            clearable
            autocomplete="off"
            :disabled="submitting"
            :placeholder="t('system.globalSettings.createUser.emailPlaceholder')"
          />
        </t-form-item>
        <t-form-item name="autoGenerate">
          <t-checkbox v-model="form.autoGenerate" :disabled="submitting">
            {{ t('system.globalSettings.createUser.autoGenerateLabel') }}
          </t-checkbox>
        </t-form-item>
        <template v-if="!form.autoGenerate">
          <t-form-item :label="t('system.globalSettings.createUser.newPasswordLabel')" name="newPassword">
            <t-input
              v-model="form.newPassword"
              type="password"
              autocomplete="new-password"
              :disabled="submitting"
              :placeholder="t('system.globalSettings.createUser.newPasswordPlaceholder')"
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </t-form-item>
          <t-form-item :label="t('system.globalSettings.createUser.confirmPasswordLabel')" name="confirmPassword">
            <t-input
              v-model="form.confirmPassword"
              type="password"
              autocomplete="new-password"
              :disabled="submitting"
              :placeholder="t('system.globalSettings.createUser.confirmPasswordPlaceholder')"
              @enter="submit"
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </t-form-item>
        </template>
      </t-form>
    </template>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { FormInstanceFunctions, FormRule } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { createSystemUser, type CreateSystemUserRequest } from '@/api/system'
import { getAuthConfig } from '@/api/auth'
import { copyWithToast } from '@/utils/clipboard'
import { newPasswordRules } from '@/utils/passwordPolicy'
import {
  applyCreateUserResponse,
  captureLockedEscape,
  resolveCreateUserView,
  shouldAcceptCreateUserSubmit,
  type CreateUserReveal,
} from './createUserDialogState'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{
  'update:visible': [boolean]
  announced: [string]
}>()

const { t } = useI18n()

const submitting = ref(false)
const formRef = ref<FormInstanceFunctions>()
const form = reactive({
  username: '',
  email: '',
  autoGenerate: true,
  newPassword: '',
  confirmPassword: '',
})
// Non-null switches the dialog to the reveal view. Cleared on the next
// open and after the close animation so the password stays on screen
// while the dialog is leaving.
const createUserSuccess = ref<CreateUserReveal | null>(null)
const locked = computed(() => submitting.value || createUserSuccess.value !== null)
const complexPasswordEnabled = ref(false)

const loadAuthConfig = async () => {
  const resp = await getAuthConfig()
  complexPasswordEnabled.value = !!resp.complex_password_enabled
  if (!resp.success) {
    MessagePlugin.warning(t('system.globalSettings.messages.loadFailed'))
  }
}

// autoGenerate hides the password fields (v-if), so they are never
// registered with the form and need no skip-guard in their rules.
const rules = computed<Record<string, FormRule[]>>(() => ({
  username: [
    { required: true, message: t('system.globalSettings.createUser.validation.usernameRequired'), trigger: 'blur' },
    { min: 2, max: 50, message: t('system.globalSettings.createUser.validation.usernameLength'), trigger: 'blur' },
  ],
  email: [
    { required: true, message: t('system.globalSettings.createUser.validation.emailRequired'), trigger: 'blur' },
    { email: true, message: t('system.globalSettings.createUser.validation.emailInvalid'), trigger: 'blur' },
  ],
  newPassword: newPasswordRules(t, complexPasswordEnabled.value),
  confirmPassword: [
    { required: true, message: t('system.globalSettings.createUser.validation.confirmRequired'), trigger: 'blur' },
    {
      validator: (value: string) => value === form.newPassword,
      message: t('system.globalSettings.createUser.validation.passwordMismatch'),
      trigger: 'blur',
    },
  ],
}))

watch(() => props.visible, async (visible) => {
  if (!visible) return
  createUserSuccess.value = null
  resetFormFields()
  await loadAuthConfig()
  await nextTick()
  formRef.value?.clearValidate?.()
})

function onVisibleChange(visible: boolean) {
  emit('update:visible', visible)
}

function onEscKeydown(ctx: { e: KeyboardEvent }) {
  captureLockedEscape(props.visible, locked.value, ctx.e)
}

function onWindowEscCapture(e: KeyboardEvent) {
  captureLockedEscape(props.visible, locked.value, e)
}

onMounted(() => {
  window.addEventListener('keydown', onWindowEscCapture, true)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onWindowEscCapture, true)
})

function resetFormFields() {
  form.username = ''
  form.email = ''
  form.autoGenerate = true
  form.newPassword = ''
  form.confirmPassword = ''
  formRef.value?.clearValidate?.()
}

function onClosed() {
  if (!props.visible) {
    createUserSuccess.value = null
  }
}

function announce(msg: string, kind: 'success' | 'error' = 'success') {
  emit('announced', msg)
  if (kind === 'error') {
    MessagePlugin.error(msg)
  } else {
    MessagePlugin.success(msg)
  }
}

async function submit() {
  if (!shouldAcceptCreateUserSubmit(submitting.value, createUserSuccess.value !== null)) return
  submitting.value = true
  try {
    const valid = await formRef.value?.validate?.()
    if (valid !== true) return

    const payload: CreateSystemUserRequest = {
      username: form.username.trim(),
      email: form.email.trim(),
    }
    if (!form.autoGenerate) {
      payload.password = form.newPassword
    }
    const response = await createSystemUser(payload)
    const view = resolveCreateUserView(
      response,
      { username: payload.username, email: payload.email },
      form.autoGenerate,
    )
    const next = applyCreateUserResponse(createUserSuccess.value, view)
    createUserSuccess.value = next.success
    if (next.notice === 'reveal') {
      emit('announced', t('system.globalSettings.createUser.success'))
    } else if (next.notice === 'created') {
      announce(t('system.globalSettings.createUser.success'))
    } else if (next.notice === 'idempotent') {
      announce(t('system.globalSettings.createUser.successIdempotent'))
    } else if (next.notice === 'missingPassword') {
      announce(t('system.globalSettings.createUser.missingPassword'), 'error')
    }
    if (next.close) {
      emit('update:visible', false)
    }
  } catch (err: any) {
    const msg = err?.message || t('system.globalSettings.createUser.failed')
    announce(msg, 'error')
  } finally {
    submitting.value = false
  }
}

async function copyGeneratedPassword() {
  if (!createUserSuccess.value) return
  await copyWithToast(
    createUserSuccess.value.generatedPassword,
    'system.globalSettings.createUser.generated.copySuccess',
  )
}

function acknowledge() {
  if (!createUserSuccess.value) return
  MessagePlugin.success(t('system.globalSettings.createUser.success'))
  emit('update:visible', false)
}
</script>

<style lang="less">
/*
 * Per-flow styles on top of the shared shell in systemAdminDialog.less
 * (imported by SystemSettings.vue).
 */
.create-user-dialog {
  .create-user-reveal {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-bottom: 20px;
  }

  .create-user-reveal-row {
    display: flex;
    align-items: baseline;
    gap: 12px;

    &--password {
      flex-direction: column;
      align-items: stretch;
      gap: 6px;
    }
  }

  .create-user-reveal-label {
    flex-shrink: 0;
    min-width: 80px;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }

  .create-user-reveal-value {
    font-size: 14px;
    color: var(--td-text-color-primary);
    word-break: break-all;
  }

  .create-user-reveal-password {
    display: flex;
    gap: 8px;
    align-items: stretch;
  }

  .create-user-reveal-password-value {
    flex: 1;
    margin: 0;
    padding: 8px 12px;
    font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
    font-size: 14px;
    line-height: 22px;
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;
    word-break: break-all;
    white-space: pre-wrap;
    user-select: all;
  }

  .create-user-acknowledge {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 16px 0;
    border-top: 1px solid var(--td-component-stroke);
  }
}
</style>
