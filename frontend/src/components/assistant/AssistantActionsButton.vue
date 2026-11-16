<template>
  <AssistantButton v-if="assistant.pageAccess" :label="label" :icon-only="iconOnly" :disabled="disabled"
    @click.stop="openAssistant" />
</template>

<script setup lang="ts">
import { nextTick } from 'vue'
import { useAssistantStore, type AssistantContextAction, type AssistantPageSelection } from '@/stores/assistant'
import AssistantButton from './AssistantButton.vue'

const props = defineProps<{
  label: string
  iconOnly?: boolean
  disabled?: boolean
  selection?: AssistantPageSelection
  actions: AssistantContextAction[]
}>()
const emit = defineEmits<{ beforeOpen: [] }>()
const assistant = useAssistantStore()

async function openAssistant() {
  if (props.disabled || !assistant.pageAccess) return
  const suggestions = { label: props.label, actions: props.actions.map(action => ({ ...action })) }
  assistant.setPageSelection(props.selection?.text.trim() ? { ...props.selection } : null)
  emit('beforeOpen')
  // Let the owning page publish the selected message/resource before opening.
  await nextTick()
  assistant.restorePageContext()
  assistant.showActionSuggestions(suggestions)
}
</script>
