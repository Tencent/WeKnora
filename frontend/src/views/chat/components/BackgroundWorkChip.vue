<template>
  <t-popup
    v-if="running.length"
    v-model:visible="open"
    trigger="click"
    placement="top-left"
    :overlay-inner-style="{ padding: '6px' }"
  >
    <button
      type="button"
      class="chip-count"
      :aria-expanded="open"
      :aria-label="$t('chat.backgroundWork.running', { count: running.length }, running.length)"
    >
      <span class="chip-badge" aria-hidden="true" />
      <span>{{ $t('chat.backgroundWork.running', { count: running.length }, running.length) }}</span>
    </button>
    <template #content>
      <div class="work-card" role="list">
        <div v-for="item in running" :key="item.work_id" class="work-row" role="listitem">
          <button type="button" class="work-main" @click="openOutput(item.work_id)">
            <span class="work-title">{{ item.title || item.work_id }}</span>
            <span v-if="elapsed(item)" class="work-duration">{{ elapsed(item) }}</span>
          </button>
          <button
            type="button"
            class="work-stop"
            :disabled="cancelling.has(item.work_id)"
            @click="onCancel(item.work_id)"
          >{{ $t('chat.backgroundWork.stop') }}</button>
        </div>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import type { BackgroundWorkItem } from '@/api/chat/background-work'

const props = defineProps<{
  items: BackgroundWorkItem[]
  cancelWork: (workId: string) => Promise<void>
}>()
const emit = defineEmits<{ view: [workId: string] }>()
const cancelling = ref(new Set<string>())
const open = ref(false)
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined

const running = computed(() => props.items.filter((item) => item.state === 'running'))

function syncClock() {
  const live = running.value.some((item) => item.started_at)
  if (live && !clock) {
    clock = setInterval(() => { now.value = Date.now() }, 1000)
  } else if (!live && clock) {
    clearInterval(clock)
    clock = undefined
  }
}

watch(running, (items) => {
  syncClock()
  if (!items.length) open.value = false
  const ids = new Set(items.map((item) => item.work_id))
  const next = new Set([...cancelling.value].filter((id) => ids.has(id)))
  if (next.size !== cancelling.value.size) cancelling.value = next
}, { deep: true, immediate: true })

onUnmounted(() => {
  if (clock) clearInterval(clock)
})

function openOutput(workId: string) {
  open.value = false
  emit('view', workId)
}

async function onCancel(workId: string) {
  if (cancelling.value.has(workId)) return
  const next = new Set(cancelling.value)
  next.add(workId)
  cancelling.value = next
  try {
    await props.cancelWork(workId)
  } finally {
    // The request may fail while the job remains running. Always permit retry
    // after it settles; only an in-flight request should disable the button.
    const remaining = new Set(cancelling.value)
    remaining.delete(workId)
    cancelling.value = remaining
  }
}

function formatDuration(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${minutes % 60}m`
}

function elapsed(item: BackgroundWorkItem): string {
  const start = Date.parse(item.started_at ?? '')
  if (!Number.isFinite(start)) return ''
  return formatDuration(now.value - start)
}
</script>

<style lang="less" scoped>
.chip-count {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  height: 28px;
  margin: 0;
  padding: 0 10px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-md);
  font-weight: 500;
  line-height: 18px;
  cursor: pointer;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);

  &:hover,
  &[aria-expanded='true'] {
    background: var(--td-bg-color-secondarycontainer-hover);
    color: var(--td-text-color-primary);
  }
}

.chip-badge {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.work-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: min(320px, 72vw);
  max-height: 280px;
  overflow: auto;
}

.work-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.work-main {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex: 1;
  min-width: 0;
  margin: 0;
  padding: 6px 8px;
  border: 0;
  border-radius: var(--td-radius-medium);
  background: transparent;
  color: var(--td-text-color-primary);
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 18px;
  text-align: left;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover);
  }
}

.work-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.work-duration {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.work-stop {
  flex-shrink: 0;
  margin: 0;
  padding: 4px 8px;
  border: 0;
  border-radius: var(--td-radius-medium);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 18px;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover);
  }

  &:disabled {
    cursor: default;
    opacity: 0.45;
  }
}
</style>
