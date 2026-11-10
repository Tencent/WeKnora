<template>
  <AssistantSelectionBubble />
  <PageFillNotice />
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { useAssistantStore } from '@/stores/assistant'
import { routeTitle } from '@/utils/assistantRouteTitle'
import { installPageAwareness } from '@/utils/pageAwareness'
import AssistantSelectionBubble from './AssistantSelectionBubble.vue'
import PageFillNotice from './PageFillNotice.vue'

// What the assistant sees of the page beside it: the page, the open dialog
// and the selection feed its input box; its edits show up on the page.
const assistant = useAssistantStore()
const route = useRoute()
const { t, locale } = useI18n()
let uninstall: (() => void) | null = null

watch(() => [route.name, route.params.section, locale.value], () => {
  assistant.setPageTitle(routeTitle(route, t))
}, { immediate: true })

// With page access off the assistant sees nothing of the page, this included.
let view = ''
onMounted(() => {
  uninstall = installPageAwareness({
    onView: next => {
      view = next
      assistant.setPageView(assistant.pageAccess ? next : '')
    },
    onSelection: selection => assistant.setPageSelection(assistant.pageAccess ? selection : null),
  })
})
watch(() => assistant.pageAccess, on => {
  assistant.setPageView(on ? view : '')
  if (!on) assistant.setPageSelection(null)
})
onUnmounted(() => {
  uninstall?.()
  assistant.setPageTitle('')
})
</script>
