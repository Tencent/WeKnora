<template>
  <t-drawer
    v-model:visible="drawerVisible"
    :header="title"
    :size="width"
    placement="right"
    destroy-on-close
  >
    <div class="setting-drawer__body">
      <p v-if="description" class="setting-drawer__desc">{{ description }}</p>
      <slot />
    </div>
    <template v-if="!hideFooter" #footer>
      <div class="setting-drawer__footer">
        <div class="setting-drawer__footer-left">
          <slot name="footer-left" />
        </div>
        <div class="setting-drawer__footer-right">
          <t-button theme="default" variant="outline" @click="handleCancel">
            {{ cancelText || t('common.cancel') }}
          </t-button>
          <t-button
            theme="primary"
            :loading="confirmLoading"
            :disabled="confirmDisabled"
            @click="handleConfirm"
          >
            {{ confirmText || t('common.save') }}
          </t-button>
        </div>
      </div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

interface Props {
  visible: boolean
  title: string
  description?: string
  width?: string
  confirmLoading?: boolean
  confirmDisabled?: boolean
  confirmText?: string
  cancelText?: string
  hideFooter?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  description: '',
  width: '500px',
  confirmLoading: false,
  confirmDisabled: false,
  confirmText: '',
  cancelText: '',
  hideFooter: false
})

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'confirm'): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()

const drawerVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const handleConfirm = () => emit('confirm')
const handleCancel = () => {
  emit('cancel')
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
.setting-drawer__body {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.setting-drawer__desc {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
}

.setting-drawer__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}

.setting-drawer__footer-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.setting-drawer__footer-right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}
</style>
