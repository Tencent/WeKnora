<template>
  <div class="js-tool-details" @click.stop>
    <div class="js-tool-meta">
      <span class="js-status" :class="`js-status--${status}`">{{ t(statusKey) }}</span>
      <span v-if="view.lineCount">{{ t('agentStream.javascript.lines', { count: view.lineCount }) }}</span>
      <span v-if="view.calls.length">{{ t('agentStream.javascript.callCount', { count: view.calls.length }) }}</span>
      <span v-if="view.failedCalls" class="js-failed">
        {{ t('agentStream.javascript.failedCount', { count: view.failedCalls }) }}
      </span>
      <span v-if="view.durationMs">{{ t('agentStream.javascript.duration', { ms: view.durationMs }) }}</span>
    </div>

    <div v-if="view.error" class="js-error">{{ view.error }}</div>

    <div v-if="showLiveProgress" class="js-live">
      <div v-for="(item, index) in liveProgress" :key="item.nested_call_id || index" class="js-live-row">
        <span class="js-live-dot" :class="liveDotClass(item)" />
        <span class="js-live-tool" :title="item.tool_name">{{ item.tool_name || '…' }}</span>
        <span class="js-live-status">{{ liveStatus(item) }}</span>
        <span v-if="item.duration_ms" class="js-live-time">{{ item.duration_ms }} ms</span>
      </div>
    </div>

    <details v-if="view.code" class="js-section" open>
      <summary class="js-section-header">
        <span class="js-section-title">{{ t('agentStream.javascript.script') }}</span>
        <button type="button" class="js-copy" @click.stop.prevent="copyCode">
          {{ t('agentStream.javascript.copy') }}
        </button>
      </summary>
      <pre class="js-code"><code>{{ view.code }}</code></pre>
    </details>

    <details v-if="view.calls.length" class="js-section" open>
      <summary class="js-section-header">
        <span class="js-section-title">{{ t('agentStream.javascript.calls') }}</span>
      </summary>
      <div class="js-calls-scroll">
        <table class="js-calls">
          <thead>
            <tr>
              <th>{{ t('agentStream.javascript.tool') }}</th>
              <th>{{ t('agentStream.javascript.status') }}</th>
              <th>{{ t('agentStream.javascript.time') }}</th>
              <th>{{ t('agentStream.javascript.chars') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(call, index) in view.calls" :key="`${call.tool}-${index}`">
              <td class="js-call-tool" :title="call.tool">{{ call.tool || '—' }}</td>
              <td>
                <span :class="call.ok === false ? 'js-call-fail' : 'js-call-ok'">
                  {{ call.ok === false ? t('agentStream.javascript.callFailed') : t('agentStream.javascript.ok') }}
                </span>
              </td>
              <td>{{ call.ms ?? 0 }} ms</td>
              <td>{{ call.chars ?? 0 }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="callErrors.length" class="js-call-errors">
        <div v-for="call in callErrors" :key="`${call.tool}-${call.error}`" class="js-call-error">
          <span class="js-call-error-tool">{{ call.tool }}:</span> {{ call.error }}
        </div>
      </div>
    </details>

    <details v-if="view.resultText !== ''" class="js-section" open>
      <summary class="js-section-header">
        <span class="js-section-title">{{ t('agentStream.javascript.result') }}</span>
      </summary>
      <pre class="js-result">{{ view.resultText }}</pre>
    </details>

    <details v-if="view.logs.length" class="js-section">
      <summary class="js-section-header">
        <span class="js-section-title">{{ t('agentStream.javascript.logs') }}</span>
      </summary>
      <pre class="js-logs">{{ view.logs.join('\n') }}</pre>
    </details>

    <details v-if="!view.payload && view.rawOutput" class="js-section" open>
      <summary class="js-section-header">
        <span class="js-section-title">{{ t('agentStream.javascript.rawOutput') }}</span>
        <span class="js-parse-warning">{{ t('agentStream.javascript.parseFailed') }}</span>
      </summary>
      <pre class="js-result">{{ view.rawOutput }}</pre>
    </details>

    <div v-if="!hasDetails" class="js-empty">{{ t('agentStream.javascript.empty') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyWithToast } from '@/utils/clipboard'
import { parseJavaScriptEvent, type JavaScriptLiveProgress } from '@/utils/javascriptTool'

const props = defineProps<{ event: any }>()
const { t } = useI18n()

const view = computed(() => parseJavaScriptEvent(props.event))
const status = computed(() => {
  if (props.event?.pending) return 'running'
  if (props.event?.success === false) return 'failed'
  return 'done'
})
const statusKey = computed(() => `agentStream.javascript.${status.value}`)
const liveProgress = computed<JavaScriptLiveProgress[]>(() => (
  Array.isArray(props.event?.js_progress) ? props.event.js_progress : []
))
const showLiveProgress = computed(() => Boolean(props.event?.pending && liveProgress.value.length))
const callErrors = computed(() => view.value.calls.filter(call => call?.error))
const hasDetails = computed(() => Boolean(
  view.value.code ||
  view.value.calls.length ||
  view.value.resultText !== '' ||
  view.value.logs.length ||
  view.value.error ||
  view.value.rawOutput,
))

const liveStatus = (item: JavaScriptLiveProgress): string => {
  if (item?.phase === 'start') return t('agentStream.javascript.running')
  if (item?.success === false) return t('agentStream.javascript.callFailed')
  return t('agentStream.javascript.ok')
}
const liveDotClass = (item: JavaScriptLiveProgress) => ({
  'is-running': item?.phase === 'start',
  'is-ok': item?.phase === 'result' && item?.success !== false,
  'is-failed': item?.phase === 'result' && item?.success === false,
})

const copyCode = () => copyWithToast(
  view.value.code,
  'agentStream.javascript.copied',
  'common.copyFailed',
)
</script>

<style scoped>
.js-tool-details {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 0 2px;
  min-width: 0;
}

.js-tool-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}

.js-status {
  border-radius: 999px;
  padding: 1px 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.js-status--running {
  color: var(--td-brand-color);
  background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
}

.js-status--done {
  color: var(--td-success-color);
  background: color-mix(in srgb, var(--td-success-color) 10%, transparent);
}

.js-status--failed,
.js-failed {
  color: var(--td-error-color);
}

.js-status--failed {
  background: color-mix(in srgb, var(--td-error-color) 10%, transparent);
}

.js-error {
  padding: 8px 10px;
  border-radius: var(--app-radius-sm);
  border: 1px solid color-mix(in srgb, var(--td-error-color) 35%, transparent);
  background: color-mix(in srgb, var(--td-error-color) 6%, transparent);
  color: var(--td-error-color);
  font-size: var(--app-text-xs);
  white-space: pre-wrap;
  word-break: break-word;
}

.js-live {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
}

.js-live-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-primary);
}

.js-live-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--td-text-color-secondary);
  flex: none;
}

.js-live-dot.is-running {
  background: var(--td-brand-color);
  animation: js-live-pulse 1s ease-in-out infinite;
}

.js-live-dot.is-ok {
  background: var(--td-success-color);
}

.js-live-dot.is-failed {
  background: var(--td-error-color);
}

.js-live-tool {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.js-live-status {
  margin-left: auto;
  color: var(--td-text-color-secondary);
}

.js-live-time {
  color: var(--td-text-color-secondary);
}

@keyframes js-live-pulse {
  0%, 100% { opacity: 0.35; }
  50% { opacity: 1; }
}

.js-section {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.js-section-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  cursor: pointer;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-xs);
  user-select: none;
}

.js-section-title {
  font-weight: 600;
}

.js-copy {
  margin-left: auto;
  border: 0;
  background: transparent;
  color: var(--td-brand-color);
  cursor: pointer;
  font-size: var(--app-text-xs);
  padding: 0;
}

.js-copy:hover {
  text-decoration: underline;
}

.js-parse-warning {
  margin-left: auto;
  color: var(--td-text-color-secondary);
}

.js-code,
.js-result,
.js-logs {
  margin: 0;
  padding: 10px;
  max-height: 360px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--app-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: var(--app-text-xs);
  line-height: 1.6;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
}

.js-calls-scroll {
  overflow-x: auto;
}

.js-calls {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--app-text-xs);
}

.js-calls th,
.js-calls td {
  padding: 6px 10px;
  text-align: left;
  border-bottom: 1px solid var(--td-component-stroke);
  white-space: nowrap;
}

.js-calls th {
  color: var(--td-text-color-secondary);
  font-weight: 500;
}

.js-calls tbody tr:last-child td {
  border-bottom: 0;
}

.js-call-tool {
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.js-call-ok {
  color: var(--td-success-color);
}

.js-call-fail {
  color: var(--td-error-color);
}

.js-call-errors {
  padding: 8px 10px;
  border-top: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-xs);
  color: var(--td-error-color);
}

.js-call-error + .js-call-error {
  margin-top: 4px;
}

.js-call-error-tool {
  font-weight: 600;
}

.js-empty {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}
</style>
