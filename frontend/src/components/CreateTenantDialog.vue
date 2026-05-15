<template>
  <!-- 自助创建新工作区弹窗。任意已登录用户均可调用 POST /api/v1/tenants
       （后端 router 已去掉 g.CrossTenant() 守卫），handler 会自动把当前
       用户 EnsureOwner 成新租户的 Owner。提交成功后通知父组件，由父组件
       决定下一步导航（一般是切到新租户）。 -->
  <t-dialog
    :visible="visible"
    :header="$t('tenant.create.dialogTitle')"
    width="480px"
    :on-confirm="handleSubmit"
    :on-close="handleClose"
    :confirm-btn="{ content: $t('tenant.create.submit'), loading: submitting, theme: 'primary' }"
    :cancel-btn="{ content: $t('tenant.create.cancel') }"
    :close-on-overlay-click="!submitting"
    :close-on-esc-keydown="!submitting"
    @update:visible="onVisibleUpdate"
  >
    <t-form ref="formRef" :data="form" :rules="formRules" @submit.prevent>
      <t-form-item :label="$t('tenant.create.nameLabel')" name="name">
        <t-input
          v-model="form.name"
          :placeholder="$t('tenant.create.namePlaceholder')"
          :maxlength="128"
          autofocus
        />
      </t-form-item>
      <t-form-item :label="$t('tenant.create.descriptionLabel')" name="description">
        <t-textarea
          v-model="form.description"
          :placeholder="$t('tenant.create.descriptionPlaceholder')"
          :maxlength="512"
          :autosize="{ minRows: 2, maxRows: 4 }"
        />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, type FormInstanceFunctions, type FormRule } from 'tdesign-vue-next'
import { createTenant, type TenantInfo } from '@/api/tenant'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  // 创建成功后由父组件决定如何导航（切换到新租户、刷新本地列表等）。
  (e: 'created', tenant: TenantInfo): void
}>()

const { t } = useI18n()

const formRef = ref<FormInstanceFunctions | null>(null)
const submitting = ref(false)

const form = reactive({
  name: '',
  description: '',
})

const formRules: Record<string, FormRule[]> = {
  name: [
    { required: true, message: t('tenant.create.nameRequired'), trigger: 'blur' },
    { max: 128, message: t('tenant.create.nameRequired'), trigger: 'blur' },
  ],
}

// 每次打开时重置表单；关闭时不动状态，让动画结束。
watch(
  () => props.visible,
  (open) => {
    if (open) {
      form.name = ''
      form.description = ''
      // 等 dialog 渲染完成再清掉之前的校验态。
      requestAnimationFrame(() => formRef.value?.clearValidate?.())
    }
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

const handleSubmit = async () => {
  if (submitting.value) return
  const validateResult = await formRef.value?.validate?.()
  if (validateResult !== true) return

  submitting.value = true
  try {
    const response = await createTenant({
      name: form.name.trim(),
      description: form.description.trim() || undefined,
    })
    if (!response.success || !response.data) {
      MessagePlugin.error(response.message || t('tenant.create.failed'))
      return
    }
    MessagePlugin.success(t('tenant.create.success'))
    emit('created', response.data)
    emit('update:visible', false)
  } catch (error: any) {
    console.error('Failed to create tenant:', error)
    MessagePlugin.error(error?.message || t('tenant.create.failed'))
  } finally {
    submitting.value = false
  }
}
</script>
