<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolder } from '@/types/knowledgeFolder'
import { flattenKnowledgeFolders } from '@/utils/knowledgeFolderTree'

const props = defineProps<{
  visible: boolean
  folders: KnowledgeFolder[]
  title: string
  disabledIds?: Set<string>
  submitting?: boolean
}>()
const emit = defineEmits<{
  'update:visible': [visible: boolean]
  confirm: [folderId: string | null]
}>()
const { t } = useI18n()
const selected = ref<string | null>(null)
watch(() => props.visible, visible => { if (visible) selected.value = null })
const options = computed(() => {
  const folderOptions = flattenKnowledgeFolders(props.folders).map(({ folder, depth }) => ({
    label: `${'　'.repeat(depth)}${folder.name}`,
    value: folder.id,
    disabled: props.disabledIds?.has(folder.id),
  }))
  return [
    { label: t('knowledgeFolder.root'), value: '__root__' },
    ...folderOptions,
  ]
})
const confirm = () => emit('confirm', selected.value === '__root__' ? null : selected.value)
</script>

<template>
  <t-dialog
    :visible="visible"
    :header="title"
    :confirm-btn="{ content: $t('common.confirm'), loading: submitting, disabled: selected === null }"
    @update:visible="emit('update:visible', $event)"
    @confirm="confirm"
  >
    <t-select v-model="selected" :options="options" :placeholder="$t('knowledgeFolder.selectTarget')" />
  </t-dialog>
</template>
