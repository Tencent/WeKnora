<template>
  <t-dialog
    :visible="visible"
    :header="$t('toolboxCategories.assignTitle', { name: resourceName })"
    :confirm-btn="{ content: $t('common.save'), loading: saving, disabled: creating, theme: 'primary' }"
    :cancel-btn="{ content: $t('common.cancel'), disabled: saving || creating }"
    :close-on-overlay-click="!saving && !creating"
    @confirm="!saving && !creating && emit('save', [...selectedIds])"
    @close="close"
    @update:visible="(value: boolean) => !value && close()"
  >
    <p class="toolbox-category-assignment__hint">{{ $t('toolboxCategories.assignHint') }}</p>
    <ToolboxCategoryPicker v-if="visible" v-model="selectedIds" :categories="categories" :disabled="saving"
      @busy="creating = $event" @created="emit('created', $event)" />
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { ToolboxCategory } from '@/api/toolbox-category'
import ToolboxCategoryPicker from './ToolboxCategoryPicker.vue'

const props = defineProps<{
  visible: boolean
  resourceName: string
  categoryIds: string[]
  categories: ToolboxCategory[]
  saving: boolean
}>()

const emit = defineEmits<{
  (event: 'update:visible', value: boolean): void
  (event: 'save', categoryIds: string[]): void
  (event: 'created', category: ToolboxCategory): void
}>()

const selectedIds = ref<string[]>([])
const creating = ref(false)

watch(() => props.visible, (visible) => {
  if (visible) selectedIds.value = [...props.categoryIds]
}, { immediate: true })

function close() {
  if (!props.saving && !creating.value) emit('update:visible', false)
}
</script>

<style scoped>
.toolbox-category-assignment__hint {
  margin: 0 0 12px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
}
</style>
