<template>
  <section class="sandbox-command" aria-live="off" :aria-busy="!progress.done"
    :aria-label="!progress.done ? $t('settings.sandbox.installCommandRunning') : undefined">
    <div class="sandbox-command-heading">
      <code class="sandbox-command-code" :class="{ 'is-running': !progress.done }" :title="progress.command">{{ progress.command }}</code>
      <span class="sandbox-command-elapsed">{{ commandElapsed }}</span>
    </div>
    <pre v-if="progress.output">{{ progress.output }}</pre>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'

const props = defineProps<{
  progress: { command: string; started_at: string; output: string; done: boolean }
}>()
const clock = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined
const commandElapsed = computed(() => {
  const started = Date.parse(props.progress.started_at)
  const seconds = Number.isFinite(started) ? Math.max(0, Math.floor((clock.value - started) / 1000)) : 0
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
})
watch(() => props.progress.done, done => {
  clearInterval(timer)
  clock.value = Date.now()
  if (!done) timer = setInterval(() => { clock.value = Date.now() }, 1000)
}, { immediate: true })
onUnmounted(() => clearInterval(timer))
</script>

<style scoped lang="less">
@import './css/chat-timeline-loading.less';

.sandbox-command {
  min-width: 0;
  margin: 8px 0;
  padding: 8px 10px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer, #f7f7f7);
  color: var(--td-text-color-secondary, #666);
  font-size: 12px;
  line-height: 1.5;

  code, pre {
    font-family: var(--td-font-family-code, monospace);
    font-size: 11px;
    line-height: 1.6;
  }

  code {
    display: block;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  pre {
    max-height: 160px;
    margin: 8px 0 0;
    padding-top: 8px;
    overflow: auto;
    overscroll-behavior: contain;
    border-top: 1px solid var(--td-component-stroke, #e7e7e7);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

}
.sandbox-command-heading {
  display: flex;
  align-items: center;
  gap: 8px;

}
.sandbox-command-elapsed {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder, #999);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
</style>
