<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getConfigSkill } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { useConfigSkillInstallProgress } from '@/composables/useConfigSkillInstallProgress'

const props = defineProps<{ data: Record<string, any> }>()
const { t } = useI18n()
const auth = useAuthStore()
const status = ref('')
const name = ref('')
const error = ref('')
const disconnected = ref(false)
const configId = computed(() => String(props.data.sandbox_config_id || ''))
const skillId = computed(() => String(props.data.skill_id || ''))
const progress = useConfigSkillInstallProgress({ onDone: () => { void refresh(run) } })
const liveEvent = computed(() => progress.eventOf(configId.value, skillId.value))
const percent = computed(() => progress.percentOf(configId.value, skillId.value))
const busy = computed(() => ['accepted', 'installing'].includes(status.value))
const label = computed(() => {
  const keys: Record<string, string> = {
    accepted: 'settings.skills.installAccepted',
    installing: 'settings.sandbox.skillStatusInstalling',
    ready: 'settings.sandbox.skillStatusReady',
    failed: 'settings.sandbox.skillStatusFailed',
    removing: 'settings.sandbox.skillStatusRemoving',
  }
  return t(keys[status.value] || 'settings.sandbox.skillStatusLabel')
})
let run = 0
let refreshVersion = 0
let timer: ReturnType<typeof setTimeout> | undefined

function stop() {
  run++
  refreshVersion++
  if (timer) clearTimeout(timer)
  timer = undefined
  progress.stopAll()
}

async function refresh(version: number) {
  if (version !== run) return
  const request = ++refreshVersion
  if (timer) clearTimeout(timer)
  timer = undefined
  try {
    const result = await getConfigSkill(configId.value, skillId.value)
    if (version !== run || request !== refreshVersion) return
    status.value = result.data.status
    name.value = result.data.name
    error.value = result.data.error || ''
    disconnected.value = false
  } catch {
    if (version !== run || request !== refreshVersion) return
    disconnected.value = true
  }
  if (busy.value && !disconnected.value) {
    // Durable status is also the fallback when an SSE connection drops.
    timer = setTimeout(() => { void refresh(version) }, 5000)
  } else {
    progress.stopAll()
  }
}

watch([configId, skillId, () => props.data.status, () => auth.currentTenantRole, () => auth.selectedTenantId], () => {
  stop()
  status.value = String(props.data.status || 'accepted')
  name.value = String(props.data.name || '')
  error.value = String(props.data.error || '')
  disconnected.value = false
  if (!configId.value || !skillId.value || !auth.hasRole('admin')) return
  if (busy.value) progress.follow(configId.value, skillId.value)
  void refresh(run)
}, { immediate: true })
onUnmounted(stop)
</script>

<template>
  <div class="install-skill-result" role="status" aria-live="polite">
    <div class="install-skill-result__status">
      <t-loading v-if="busy && !disconnected" size="small" />
      <span v-if="name">{{ name }}</span>
      <span>{{ label }}<template v-if="busy && percent !== null"> · {{ percent }}%</template></span>
    </div>
    <t-progress v-if="busy && percent !== null" :percentage="percent" :label="false" />
    <p v-if="error" class="install-skill-result__error">{{ error }}</p>
    <p v-else-if="disconnected">{{ $t('settings.skills.loadFailed') }}</p>
    <p v-else-if="busy && liveEvent?.log">{{ liveEvent.log }}</p>
  </div>
</template>

<style scoped>
.install-skill-result { margin: 8px 0; font-size: 12px; color: var(--td-text-color-secondary); }
.install-skill-result__status { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.install-skill-result p { margin: 6px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.install-skill-result__error { color: var(--td-error-color); }
</style>
