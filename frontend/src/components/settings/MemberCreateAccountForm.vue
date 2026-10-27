<template>
  <div class="member-create-account">
    <p class="member-create-account__hint">{{ $t('tenantMember.create.description') }}</p>
    <t-form ref="formRef" :data="form" :rules="rules" :label-width="96" class="member-create-account__form">
      <t-form-item :label="$t('tenantMember.create.usernameLabel')" name="username">
        <t-input v-model="form.username" :placeholder="$t('tenantMember.create.usernamePlaceholder')" clearable />
      </t-form-item>
      <t-form-item :label="$t('tenantMember.create.emailLabel')" name="email">
        <t-input v-model="form.email" :placeholder="$t('tenantMember.create.emailPlaceholder')" clearable />
      </t-form-item>
      <t-form-item :label="$t('tenantMember.create.passwordLabel')" name="password">
        <t-input v-model="form.password" type="password" autocomplete="new-password"
          :placeholder="$t('tenantMember.create.passwordPlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('tenantMember.create.roleLabel')" name="role">
        <t-select v-model="form.role" :options="roleOptions" :popup-props="roleSelectPopupProps" />
      </t-form-item>
    </t-form>
    <div class="member-create-account__footer">
      <t-button variant="outline" :disabled="submitting" @click="emit('cancel')">
        {{ $t('common.cancel') }}
      </t-button>
      <t-button theme="primary" :loading="submitting" @click="submit">
        {{ $t('tenantMember.create.submit') }}
      </t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { createMemberAccount, type TenantRole } from '@/api/tenant/members'
import { allowedCreateRoles } from '@/utils/memberRoles'

const props = defineProps<{
  tenantId: number
  actorRole: TenantRole
  /** Owner 邀请遇到 404 时带着刚输入的邮箱切过来，表单里预填。 */
  initialEmail?: string
}>()

const emit = defineEmits<{
  (e: 'created'): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()
const formRef = ref<any>(null)
const submitting = ref(false)

const roleOptions = computed(() =>
  allowedCreateRoles(props.actorRole).map((role) => ({
    label: t('tenantMember.role.' + role),
    value: role,
  })),
)

// Owner 默认建「编辑」账号（和邀请弹窗的默认角色一致）；Admin 只有 viewer。
function defaultCreateRole(actorRole: TenantRole): TenantRole {
  const allowed = allowedCreateRoles(actorRole)
  return allowed.includes('contributor') ? 'contributor' : (allowed[0] ?? 'viewer')
}

const form = reactive<{ username: string; email: string; password: string; role: TenantRole }>({
  username: '',
  email: props.initialEmail ?? '',
  password: '',
  role: defaultCreateRole(props.actorRole),
})

// 弹层是 destroy-on-close，正常每次重开都会重建；这两个 watch 只是兜底：
// 同一实例内邮箱被重新预填、或操作者角色变化时保持表单可用。
watch(
  () => props.initialEmail,
  (email) => {
    if (email) form.email = email
  },
)
watch(
  () => props.actorRole,
  (role) => {
    const allowed = allowedCreateRoles(role)
    if (!allowed.includes(form.role)) form.role = defaultCreateRole(role)
  },
)

const rules = {
  username: [
    { required: true, message: t('tenantMember.create.errors.usernameRequired'), trigger: 'blur' },
    { min: 2, max: 50, message: t('tenantMember.create.errors.usernameLength'), trigger: 'blur' },
  ],
  email: [
    { required: true, message: t('tenantMember.create.errors.emailRequired'), trigger: 'blur' },
    { email: true, message: t('tenantMember.create.errors.emailFormat'), trigger: 'blur' },
  ],
  password: [
    { required: true, message: t('tenantMember.create.errors.passwordRequired'), trigger: 'blur' },
    { min: 8, max: 32, message: t('tenantMember.create.errors.passwordLength'), trigger: 'blur' },
  ],
  role: [
    { required: true, message: t('tenantMember.create.errors.roleRequired'), trigger: 'change' },
  ],
}

/** 角色下拉挂到 body，z-index 要压过邀请浮层（3050），和成员表里的下拉一致。 */
const roleSelectPopupProps = {
  zIndex: 6200,
  overlayClassName: 'tenant-members-role-select-popup',
}

// 错误提示以后端状态码为准：409 区分邮箱/用户名占用，400 区分密码策略与参数。
function errorText(err: any): string {
  const status = err?.status
  const serverMessage = typeof err?.message === 'string' ? err.message : ''
  if (status === 409) {
    return /username/i.test(serverMessage)
      ? t('tenantMember.create.errors.usernameExists')
      : t('tenantMember.create.errors.emailExists')
  }
  if (status === 400) {
    return /password/i.test(serverMessage)
      ? t('tenantMember.create.errors.passwordPolicy')
      : t('tenantMember.create.errors.invalidInput')
  }
  if (status === 403) return t('tenantMember.create.errors.forbidden')
  return serverMessage || t('tenantMember.create.errors.generic')
}

async function submit() {
  if (submitting.value) return
  const valid = await formRef.value?.validate?.()
  if (valid !== true) return
  if (!props.tenantId) return

  submitting.value = true
  try {
    const resp = await createMemberAccount(props.tenantId, {
      username: form.username.trim(),
      email: form.email.trim(),
      password: form.password,
      role: form.role,
    })
    if (resp.success) {
      // 密码只用于这次请求，成功后立刻从内存里清掉。
      form.password = ''
      emit('created')
      return
    }
    MessagePlugin.error(resp.message || t('tenantMember.create.errors.generic'))
  } catch (err: any) {
    MessagePlugin.error(errorText(err))
  } finally {
    submitting.value = false
  }
}
</script>

<style lang="less" scoped>
.member-create-account__hint {
  margin: 0 0 12px;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.member-create-account__form {
  :deep(.t-form__item) {
    margin-bottom: 14px;

    &:last-child {
      margin-bottom: 4px;
    }
  }
}

.member-create-account__footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}
</style>
