<template>
  <div class="shell-exec-result">
    <div v-if="view.command" class="shell-exec-command">
      <span class="shell-exec-prompt" aria-hidden="true">$</span>
      <pre class="shell-exec-command-text">{{ view.command }}</pre>
    </div>

    <div v-if="hasMeta" class="shell-exec-meta">
      <span v-if="view.workDir">{{ $t('agentStream.shellExec.workDir') }} {{ view.workDir }}</span>
      <span v-if="view.exitCode !== null" :class="{ 'is-error': view.exitCode !== 0 }">
        {{ $t('agentStream.shellExec.exitCode') }} {{ view.exitCode }}
      </span>
      <span v-if="durationLabel">{{ durationLabel }}</span>
      <span v-if="view.killed">{{ $t('agentStream.shellExec.killed') }}</span>
      <span v-if="view.truncated">{{ $t('agentStream.shellExec.truncated') }}</span>
    </div>

    <div v-if="view.stdoutBinary || view.stderrBinary" class="shell-exec-note">
      {{ $t('agentStream.shellExec.binarySuppressed') }}
    </div>

    <div v-if="view.stdout" class="shell-exec-stream">
      <div class="shell-exec-stream-label">{{ $t('agentStream.shellExec.stdout') }}</div>
      <pre class="shell-exec-stream-body">{{ view.stdout }}</pre>
    </div>

    <div v-if="view.stderr" class="shell-exec-stream is-stderr">
      <div class="shell-exec-stream-label">{{ $t('agentStream.shellExec.stderr') }}</div>
      <pre class="shell-exec-stream-body">{{ view.stderr }}</pre>
    </div>

    <div v-if="!view.stdout && !view.stderr && !view.stdoutBinary && !view.stderrBinary" class="shell-exec-empty">
      {{ $t('agentStream.shellExec.emptyOutput') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ShellExecData } from '@/types/tool-results'
import { buildShellExecView } from '@/utils/shellExecResult'

const props = defineProps<{
  data: ShellExecData | Record<string, unknown>
  output?: string
  arguments?: Record<string, unknown>
}>()

const view = computed(() =>
  buildShellExecView(props.data as Record<string, unknown>, props.arguments, props.output),
)

const hasMeta = computed(() =>
  Boolean(
    view.value.workDir ||
    view.value.exitCode !== null ||
    view.value.durationMs !== null ||
    view.value.killed ||
    view.value.truncated,
  ),
)

const durationLabel = computed(() => {
  const ms = view.value.durationMs
  if (ms == null) return ''
  if (ms < 1000) return `${Math.round(ms)}ms`
  const seconds = ms / 1000
  if (seconds < 10) return `${seconds.toFixed(1)}s`
  return `${Math.round(seconds)}s`
})
</script>

<style lang="less" scoped>
.shell-exec-result {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.shell-exec-command {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
}

.shell-exec-prompt {
  flex-shrink: 0;
  margin-top: 1px;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.55;
  color: var(--td-text-color-placeholder);
}

.shell-exec-command-text {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.shell-exec-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);

  .is-error {
    color: var(--td-error-color);
  }
}

.shell-exec-note,
.shell-exec-empty {
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
}

.shell-exec-stream {
  min-width: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  overflow: hidden;
  background: var(--td-bg-color-container);

  &.is-stderr .shell-exec-stream-label {
    color: var(--td-error-color);
  }
}

.shell-exec-stream-label {
  padding: 6px 12px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
}

.shell-exec-stream-body {
  margin: 0;
  padding: 10px 12px;
  max-height: 280px;
  overflow: auto;
  font-family: var(--app-font-family-mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;

  &::-webkit-scrollbar {
    width: 8px;
    height: 8px;
  }

  &::-webkit-scrollbar-thumb {
    background: var(--td-component-border);
    border-radius: 4px;
  }
}
</style>
