<template>
  <div class="setting-row">
    <div class="setting-info">
      <label>{{ $t('settings.hostApprovalDefault.title') }}</label>
      <p class="desc">{{ $t('settings.hostApprovalDefault.description') }}</p>
    </div>
    <div class="setting-control">
      <t-radio-group :value="mode" :disabled="saving" @change="onChange">
        <t-radio-button v-for="m in HOST_APPROVAL_MODES" :key="m" :value="m">
          {{ $t(`input.hostApprovalMode.${m}`) }}
        </t-radio-button>
      </t-radio-group>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { HOST_APPROVAL_MODES, parseHostApprovalMode, type HostApprovalMode } from '@/components/hostApprovalMode'

const { t } = useI18n()
const mode = ref<HostApprovalMode>('ask')
const saving = ref(false)

const nativeApp = () => (window as any).go?.main?.App

onMounted(async () => {
  try {
    mode.value = parseHostApprovalMode(await nativeApp()?.GetApprovalMode?.())
  } catch {
    mode.value = 'ask'
  }
})

async function onChange(value: unknown) {
  const next = parseHostApprovalMode(value)
  const previous = mode.value
  mode.value = next
  saving.value = true
  try {
    const app = nativeApp()
    if (!app?.SetApprovalMode) throw new Error('desktop bridge unavailable')
    await app.SetApprovalMode(next)
  } catch {
    mode.value = previous
    MessagePlugin.error(t('input.hostApprovalMode.switchFailed'))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped lang="less">
@import (reference) '@/components/css/settings-section.less';

.setting-row {
  .setting-row();
}

.setting-info {
  .setting-info();
}

.setting-control {
  .setting-control();
}
</style>
