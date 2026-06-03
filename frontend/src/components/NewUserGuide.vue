<template>
  <SpotlightGuide v-model:active="active" :steps="steps" step-i18n-prefix="newUserGuide.steps"
    labels-prefix="newUserGuide" @finish="onFinish" @step-change="onStepChange" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import { GLOBAL_USER_GUIDE_KEY } from '@/config/contextualGuides'
import { useUIStore } from '@/stores/ui'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const OPEN_EVENT = 'weknora:open-new-user-guide'

const uiStore = useUIStore()
let settingsOpenedByGuide = false

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

const open = () => {
  active.value = true
}

const handleOpenEvent = () => {
  if (active.value) return
  open()
}

onMounted(() => {
  window.addEventListener(OPEN_EVENT, handleOpenEvent)
  if (localStorage.getItem(GLOBAL_USER_GUIDE_KEY) !== '1') {
    window.setTimeout(() => {
      if (localStorage.getItem(GLOBAL_USER_GUIDE_KEY) !== '1') {
        open()
      }
    }, 700)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener(OPEN_EVENT, handleOpenEvent)
  closeGuideSettings()
})
</script>
