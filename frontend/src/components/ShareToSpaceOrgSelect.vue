<template>
  <t-select
    :model-value="modelValue"
    :placeholder="placeholder || $t('organization.share.selectOrgPlaceholder')"
    :loading="loading"
    class="share-org-select"
    :popup-props="orgSelectPopupProps"
    @update:model-value="$emit('update:modelValue', $event)"
    @popup-visible-change="handlePopupVisibleChange"
  >
    <t-option
      v-for="org in organizations"
      :key="org.id"
      :value="org.id"
      :label="org.name"
    >
      <div class="share-org-option">
        <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
        <span class="share-org-option-name">{{ org.name }}</span>
        <span v-if="roleLabel(org)" class="share-org-option-role">{{ roleLabel(org) }}</span>
      </div>
    </t-option>
  </t-select>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { Organization } from '@/api/organization'
import SpaceAvatar from '@/components/SpaceAvatar.vue'

defineProps<{
  modelValue: string
  organizations: Organization[]
  loading?: boolean
  placeholder?: string
}>()

defineEmits<{
  'update:modelValue': [value: string]
}>()

const { t } = useI18n()

const orgSelectPopupProps = {
  attach: 'body' as const,
  overlayClassName: 'share-org-select-popup',
  zIndex: 3060,
}

function handlePopupVisibleChange(visible: boolean) {
  if (!visible) return
  requestAnimationFrame(() => {
    document.querySelectorAll('.share-org-select-popup .t-select-option[title]').forEach((el) => {
      el.removeAttribute('title')
    })
  })
}

function roleLabel(org: Organization) {
  if (org.is_owner) return t('organization.owner')
  if (org.my_role) return t(`organization.role.${org.my_role}`)
  return ''
}
</script>

<style scoped lang="less">
.share-org-select {
  width: 100%;
}
</style>
