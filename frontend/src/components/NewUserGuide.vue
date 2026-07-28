<template>
  <SpotlightGuide v-model:active="active" :steps="steps" step-i18n-prefix="newUserGuide.steps"
    labels-prefix="newUserGuide" @finish="onFinish" @dismiss="onFinish" @step-change="onStepChange" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import { GLOBAL_USER_GUIDE_KEY, OPEN_NEW_USER_GUIDE_EVENT } from '@/config/contextualGuides'
import { useUIStore } from '@/stores/ui'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'
import { useResponsiveViewport } from '@/composables/useResponsiveViewport'

const uiStore = useUIStore()
const { isCompact } = useResponsiveViewport()
let settingsOpenedByGuide = false
let autoOpenTimer: number | null = null

const steps = computed<SpotlightGuideStep[]>(() => [
  { key: 'welcome' },
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

const active = ref(false)

const closeGuideSettings = () => {
  if (settingsOpenedByGuide) {
    uiStore.closeSettings()
    settingsOpenedByGuide = false
  }
}

const onFinish = () => {
  localStorage.setItem(GLOBAL_USER_GUIDE_KEY, '1')
  closeGuideSettings()
}

const onStepChange = ({ toKey }: { toKey: string }) => {
  if (toKey !== 'models') {
    closeGuideSettings()
  }
}

const clearAutoOpenTimer = () => {
  if (autoOpenTimer) {
    clearTimeout(autoOpenTimer)
    autoOpenTimer = null
  }
}

const open = () => {
  if (isCompact.value) return
  active.value = true
}

const scheduleAutoOpen = () => {
  clearAutoOpenTimer()
  if (isCompact.value || localStorage.getItem(GLOBAL_USER_GUIDE_KEY) === '1') return
  autoOpenTimer = window.setTimeout(() => {
    autoOpenTimer = null
    if (!isCompact.value && localStorage.getItem(GLOBAL_USER_GUIDE_KEY) !== '1') open()
  }, 700)
}

const handleOpenEvent = () => {
  if (active.value) return
  open()
}

onMounted(() => {
  window.addEventListener(OPEN_NEW_USER_GUIDE_EVENT, handleOpenEvent)
  scheduleAutoOpen()
})

watch(isCompact, (compact) => {
  if (compact) {
    clearAutoOpenTimer()
    active.value = false
    closeGuideSettings()
  } else {
    scheduleAutoOpen()
  }
})

onBeforeUnmount(() => {
  window.removeEventListener(OPEN_NEW_USER_GUIDE_EVENT, handleOpenEvent)
  clearAutoOpenTimer()
  closeGuideSettings()
})
</script>
