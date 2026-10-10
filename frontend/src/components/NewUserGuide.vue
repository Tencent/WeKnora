<template>
  <SpotlightGuide v-model:active="active" :steps="steps" step-i18n-prefix="newUserGuide.steps"
    labels-prefix="newUserGuide" @finish="onFinish" @step-change="onStepChange" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import { GLOBAL_USER_GUIDE_KEY, OPEN_NEW_USER_GUIDE_EVENT } from '@/config/contextualGuides'
import { useUIStore } from '@/stores/ui'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

const uiStore = useUIStore()
function revealNavigation() {
  if (window.matchMedia('(max-width: 767px)').matches) window.dispatchEvent(new Event('weknora:open-mobile-navigation'))
  else uiStore.expandSidebar()
}
let settingsOpenedByGuide = false

const steps = computed<SpotlightGuideStep[]>(() => [
  { key: 'welcome' },
  {
    key: 'knowledge',
    target: '[data-guide="nav-knowledge-bases"]',
    placement: 'right',
    before: revealNavigation,
  },
  {
    key: 'agents',
    target: '[data-guide="nav-agents"]',
    placement: 'right',
    optional: true,
    before: revealNavigation,
  },
  {
    key: 'chat',
    target: '[data-guide="nav-creatChat"]',
    placement: 'right',
    before: revealNavigation,
  },
  {
    key: 'settings',
    target: '[data-guide="user-menu"]',
    placement: 'right',
    before: revealNavigation,
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
  window.addEventListener(OPEN_NEW_USER_GUIDE_EVENT, handleOpenEvent)
  if (localStorage.getItem(GLOBAL_USER_GUIDE_KEY) !== '1') {
    window.setTimeout(() => {
      if (localStorage.getItem(GLOBAL_USER_GUIDE_KEY) !== '1') {
        open()
      }
    }, 700)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener(OPEN_NEW_USER_GUIDE_EVENT, handleOpenEvent)
  closeGuideSettings()
})
</script>
