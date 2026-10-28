<template>
  <!-- 重置成员密码弹窗：管理员自填新密码 + 确认密码（01-spec AC-11）。
       成功后后端会吊销对方全部 token，旧密码和旧会话立即失效。 -->
  <t-dialog :visible="visible" width="440px" :on-confirm="handleSubmit" :on-close="handleClose"
    :confirm-btn="{ content: $t('tenantMember.resetPassword.submit'), loading: submitting, theme: 'primary' }"
    :cancel-btn="{ content: $t('common.cancel') }" :close-on-overlay-click="!submitting"
    :close-on-esc-keydown="!submitting" @update:visible="onVisibleUpdate">
    <template #header>
      <span class="member-reset-password__header">
        <t-icon name="lock-on" size="20px" class="member-reset-password__header-icon" aria-hidden="true" />
        <span>{{ $t('tenantMember.resetPassword.dialogTitle') }}</span>
      </span>
    </template>

    <p class="member-reset-password__description">
      {{ $t('tenantMember.resetPassword.description', { name: targetName }) }}
    </p>

    <t-form ref="formRef" :data="form" :rules="rules" label-align="top"
      class="member-reset-password__form" @submit.prevent>
      <t-form-item :label="$t('tenantMember.resetPassword.newPasswordLabel')" name="newPassword">
        <t-input v-model="form.newPassword" type="password" autocomplete="new-password"
          :placeholder="$t('tenantMember.resetPassword.newPasswordPlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('tenantMember.resetPassword.confirmPasswordLabel')" name="confirmPassword">
        <t-input v-model="form.confirmPassword" type="password" autocomplete="new-password"
          :placeholder="$t('tenantMember.resetPassword.confirmPasswordPlaceholder')" @enter="handleSubmit" />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, type FormInstanceFunctions, type FormRule } from 'tdesign-vue-next'
import { resetMemberPassword, type TenantMember } from '@/api/tenant/members'

const props = defineProps<{
  visible: boolean
  tenantId: number
  /** 被重置的成员；弹窗关闭期间允许为空。 */
  member: Pick<TenantMember, 'user_id' | 'username' | 'email' | 'role'> | null
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'reset'): void
}>()

const { t } = useI18n()
const formRef = ref<FormInstanceFunctions | null>(null)
const submitting = ref(false)

const form = reactive({
  newPassword: '',
  confirmPassword: '',
})

// 弹窗标题下的说明里显示对方是谁，避免对错人重置。
const targetName = computed(
  () => props.member?.username?.trim() || props.member?.email?.trim() || '—',
)

// 确认密码与上方新密码必须一致；长度沿用后端密码策略 8–32 位。
const rules = computed<Record<string, FormRule[]>>(() => ({
  newPassword: [
    { required: true, message: t('tenantMember.resetPassword.errors.passwordRequired'), trigger: 'blur' },
    { min: 8, max: 32, message: t('tenantMember.resetPassword.errors.passwordLength'), trigger: 'blur' },
  ],
  confirmPassword: [
    { required: true, message: t('tenantMember.resetPassword.errors.confirmRequired'), trigger: 'blur' },
    {
      validator: (val: string) => (val ?? '') === form.newPassword,
      message: t('tenantMember.resetPassword.errors.passwordMismatch'),
      trigger: 'blur',
    },
  ],
}))

// 每次打开都清空上一次的输入，避免把旧密码留在表单里。
watch(
  () => props.visible,
  (open) => {
    if (!open) return
    form.newPassword = ''
    form.confirmPassword = ''
    void nextTick(() => formRef.value?.clearValidate?.())
  },
)

const onVisibleUpdate = (next: boolean) => {
  if (!next && submitting.value) return
  emit('update:visible', next)
}

const handleClose = () => {
  if (submitting.value) return
  emit('update:visible', false)
}

// 错误映射以状态码为准：400 区分「重置本人 / 密码不合规」，
// 403 越权，404 目标不是当前空间成员。
function errorText(err: any): string {
  const status = err?.status
  const serverMessage = typeof err?.message === 'string' ? err.message : ''
  if (status === 400) {
    if (/your own/i.test(serverMessage)) {
      return t('tenantMember.resetPassword.errors.cannotResetSelf')
    }
    if (/password/i.test(serverMessage)) {
      return t('tenantMember.resetPassword.errors.passwordPolicy')
    }
    return t('tenantMember.resetPassword.errors.invalidInput')
  }
  if (status === 403) return t('tenantMember.resetPassword.errors.forbidden')
  if (status === 404) return t('tenantMember.resetPassword.errors.notFound')
  return serverMessage || t('tenantMember.resetPassword.errors.generic')
}

async function handleSubmit() {
  if (submitting.value) return
  const valid = await formRef.value?.validate?.()
  if (valid !== true) return
  const target = props.member
  if (!props.tenantId || !target?.user_id) return

  submitting.value = true
  try {
    const resp = await resetMemberPassword(props.tenantId, target.user_id, form.newPassword)
    if (resp.success) {
      // 密码只用于这次请求，成功后立刻从内存里清掉。
      form.newPassword = ''
      form.confirmPassword = ''
      MessagePlugin.success(t('tenantMember.resetPassword.success'))
      emit('reset')
      emit('update:visible', false)
      return
    }
    MessagePlugin.error(resp.message || t('tenantMember.resetPassword.errors.generic'))
  } catch (err: any) {
    MessagePlugin.error(errorText(err))
  } finally {
    submitting.value = false
  }
}
</script>

<style lang="less" scoped>
.member-reset-password__header {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.member-reset-password__header-icon {
  flex-shrink: 0;
  color: var(--td-brand-color);
}

.member-reset-password__description {
  margin: 0 0 16px;
  font-size: var(--app-text-md);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.member-reset-password__form {
  :deep(.t-form__item):last-child {
    margin-bottom: 0;
  }
}
</style>
