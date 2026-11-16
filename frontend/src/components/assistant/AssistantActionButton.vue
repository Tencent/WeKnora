<template>
  <AssistantButton v-if="assistant.pageAccess" :label="label" :disabled="disabled" :icon-only="iconOnly"
    :title="iconOnly ? label : assistant.displayName || t('assistant.title')" @click.stop="askAssistant" />
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useAssistantStore, type AssistantPageSelection } from '@/stores/assistant'
import AssistantButton from './AssistantButton.vue'

const { t } = useI18n()
const assistant = useAssistantStore()
const props = defineProps<{
  label: string
  query: string
  iconOnly?: boolean
  selection?: AssistantPageSelection
  disabled?: boolean
}>()

function askAssistant() {
  assistant.setPageSelection(props.selection?.text.trim() ? { ...props.selection } : null)
  assistant.restorePageContext()
  assistant.ask(props.query)
}
</script>
