<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { KnowledgeFolder } from '@/types/knowledgeFolder'
import { flattenKnowledgeFolders } from '@/utils/knowledgeFolderTree'

const props = defineProps<{
  visible: boolean
  knowledgeBaseName: string
  folders: KnowledgeFolder[]
  modelValue: string[]
  loading?: boolean
}>()
const emit = defineEmits<{
  'update:visible': [visible: boolean]
  confirm: [folderIds: string[]]
}>()
const selected = ref<string[]>([])
watch(() => props.visible, visible => {
  if (visible) selected.value = [...props.modelValue]
})
const options = computed(() => flattenKnowledgeFolders(props.folders).map(({ folder, depth }) => ({
  label: `${'　'.repeat(depth)}${folder.name}`,
  value: folder.id,
})))
</script>

<template>
  <t-dialog
    :visible="visible"
    :header="$t('knowledgeFolder.scopeTitle', { name: knowledgeBaseName })"
    @update:visible="emit('update:visible', $event)"
    @confirm="emit('confirm', selected)"
  >
    <p class="scope-hint">{{ $t('knowledgeFolder.scopeHint') }}</p>
    <div v-if="loading" class="scope-state"><t-loading /></div>
    <t-checkbox-group v-else v-model="selected" :options="options" class="scope-options" />
    <div v-if="!loading && folders.length === 0" class="scope-state">{{ $t('knowledgeFolder.empty') }}</div>
    <template #footer>
      <t-button variant="outline" @click="selected = []">{{ $t('knowledgeFolder.entireKnowledgeBase') }}</t-button>
      <t-button theme="primary" @click="emit('confirm', selected)">{{ $t('common.confirm') }}</t-button>
    </template>
  </t-dialog>
</template>

<style scoped lang="less">
.scope-hint { margin: 0 0 12px; color: var(--td-text-color-secondary); }
.scope-options { max-height: 360px; overflow: auto; display: flex; flex-direction: column; gap: 8px; }
.scope-state { min-height: 100px; display: flex; align-items: center; justify-content: center; color: var(--td-text-color-placeholder); }
</style>
