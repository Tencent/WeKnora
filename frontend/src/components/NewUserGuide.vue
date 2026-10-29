<template>
  <SpotlightGuide v-model:active="active" :steps="steps" step-i18n-prefix="newUserGuide.steps"
    :labels-prefix="assistantOnly ? 'assistantEntryGuide' : 'newUserGuide'"
    @finish="onFinish" @step-change="onStepChange" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import {
  ASSISTANT_ENTRY_GUIDE_KEY,
  GLOBAL_USER_GUIDE_KEY,
  OPEN_NEW_USER_GUIDE_EVENT,
  isGlobalUserGuideDone,
} from '@/config/contextualGuides'
import { useAssistantStore } from '@/stores/assistant'
import { useUIStore } from '@/stores/ui'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const uiStore = useUIStore()
const assistant = useAssistantStore()
const active = ref(false)
const assistantOnly = ref(false)
let openTimer: ReturnType<typeof setTimeout> | null = null
let settingsOpenedByGuide = false

const assistantStep = computed<SpotlightGuideStep>(() => ({
  key: 'assistant',
  target: '[data-guide="assistant-entry"]',
  placement: assistant.entry === 'launcher' ? 'left' : 'right',
  interact: assistantOnly.value,
  optional: !assistantOnly.value,
}))

const steps = computed<SpotlightGuideStep[]>(() => assistantOnly.value ? [assistantStep.value] : [
  { key: 'welcome' },
  ...(assistant.entry !== 'palette' ? [assistantStep.value] : []),
  {
    key: 'knowledge',
    target: '[data-guide="nav-knowledge-bases"]',
    placement: 'right',
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'agents',
    target: '[data-guide="nav-agents"]',
    placement: 'right',
    optional: true,
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'chat',
    target: '[data-guide="nav-creatChat"]',
    placement: 'right',
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'settings',
    target: '[data-guide="user-menu"]',
    placement: 'right',
    before: () => uiStore.expandSidebar(),
  },
  {
    key: 'models',
    target: '[data-guide="settings-add-model"], [data-guide="settings-models"]',
    placement: 'left',
    before: () => {
      uiStore.openSettings('models')
      settingsOpenedByGuide = true
    },
  },
  { key: 'done' },
])

const closeGuideSettings = () => {
  if (settingsOpenedByGuide) {
    uiStore.closeSettings()
    settingsOpenedByGuide = false
  }
}

const onFinish = () => {
  localStorage.setItem(GLOBAL_USER_GUIDE_KEY, '1')
  localStorage.setItem(ASSISTANT_ENTRY_GUIDE_KEY, '1')
  closeGuideSettings()
}

const onStepChange = ({ toKey }: { toKey: string }) => {
  if (toKey !== 'models') {
    closeGuideSettings()
  }
}

const handleOpenEvent = () => {
  if (active.value) return
  if (openTimer) clearTimeout(openTimer)
  assistantOnly.value = false
  active.value = true
}

const scheduleAutoGuide = () => {
  if (openTimer) clearTimeout(openTimer)
  openTimer = null
  if (active.value || isGlobalUserGuideDone()) return

  const upgrading = localStorage.getItem(GLOBAL_USER_GUIDE_KEY) === '1'
  if (upgrading && (assistant.visible || assistant.entry === 'palette')) {
    // The user has already opened the assistant, or chose to hide its entry.
    localStorage.setItem(ASSISTANT_ENTRY_GUIDE_KEY, '1')
    return
  }
  if (uiStore.showSettingsModal) return

  openTimer = setTimeout(() => {
    openTimer = null
    if (active.value || isGlobalUserGuideDone()) return
    assistantOnly.value = upgrading
    active.value = true
  }, 700)
}

watch(
  () => [assistant.visible, assistant.entry, uiStore.showSettingsModal],
  () => {
    if (active.value && assistantOnly.value && (assistant.visible || assistant.entry === 'palette')) {
      active.value = false
      onFinish()
      return
    }
    scheduleAutoGuide()
  },
)

onMounted(() => {
  window.addEventListener(OPEN_NEW_USER_GUIDE_EVENT, handleOpenEvent)
  scheduleAutoGuide()
})

onBeforeUnmount(() => {
  if (openTimer) clearTimeout(openTimer)
  window.removeEventListener(OPEN_NEW_USER_GUIDE_EVENT, handleOpenEvent)
  closeGuideSettings()
})
</script>
