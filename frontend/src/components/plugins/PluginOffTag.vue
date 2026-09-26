<template>
  <t-tooltip v-if="off" :content="t('pluginOff.hint')">
    <t-tag size="small" variant="light" theme="warning" class="plugin-off-tag">{{ t('pluginOff.label') }}</t-tag>
  </t-tooltip>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ExtensionPoint } from '@/api/plugin'
import { usePluginPagesStore } from '@/stores/pluginPages'

// Marks an instance whose type comes from a plugin the workspace switched
// off: it keeps working, but no new ones of the type can be made.
const props = defineProps<{ point: ExtensionPoint; typeId?: string }>()
const { t } = useI18n()
const store = usePluginPagesStore()

onMounted(() => {
  void store.ensure().catch(() => {})
})

const off = computed(() => !!store.switchedOff(props.point, props.typeId))
</script>

<style lang="less" scoped>
.plugin-off-tag {
  flex-shrink: 0;
}
</style>
