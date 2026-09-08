<template>
  <section class="workbench-terminal" :aria-label="t('workbench.terminal')">
    <div class="workbench-toolbar">
      <span class="terminal-state" role="status">{{ t(`workbench.connection.${phase}`) }}</span>
      <t-button v-if="phase === 'disconnected'" size="small" shape="square" variant="text" :title="t('workbench.connect')" :aria-label="t('workbench.connect')" @click="connect">
        <template #icon><t-icon name="link" /></template>
      </t-button>
      <t-button v-else size="small" shape="square" variant="text" :title="t('workbench.disconnect')" :aria-label="t('workbench.disconnect')" @click="connection?.disconnect()">
        <template #icon><t-icon name="poweroff" /></template>
      </t-button>
      <t-button size="small" shape="square" variant="text" :title="t('workbench.clearOutput')" :aria-label="t('workbench.clearOutput')" @click="clearOutput">
        <template #icon><t-icon name="clear" /></template>
      </t-button>
    </div>
    <div v-if="error" class="workbench-error" role="alert">{{ error }}</div>
    <div ref="terminalHost" class="terminal-host" :aria-label="t('workbench.terminalOutput')" />
    <div class="terminal-result" role="status">
      <span v-if="truncated">{{ t('workbench.outputLimit') }}</span>
      <span v-else-if="lastExit">{{ t('workbench.exitCode', { code: lastExit.exit_code }) }}<template v-if="lastExit.reason"> · {{ lastExit.reason }}</template></span>
    </div>
    <form class="command-form" @submit.prevent="run">
      <t-textarea
        v-model="command" :disabled="phase !== 'ready'" :autosize="{ minRows: 1, maxRows: 4 }"
        :placeholder="t('workbench.command')" :aria-label="t('workbench.command')"
        @keydown.ctrl.enter.prevent="run" @keydown.meta.enter.prevent="run"
      />
      <t-button v-if="phase === 'running' || phase === 'starting'" shape="square" variant="outline" :title="t('workbench.interrupt')" :aria-label="t('workbench.interrupt')" @click="connection?.interrupt()">
        <template #icon><t-icon name="stop-circle" /></template>
      </t-button>
      <t-button v-else type="submit" shape="square" :title="t('workbench.run')" :aria-label="t('workbench.run')" :disabled="phase !== 'ready' || !command.trim()">
        <template #icon><t-icon name="play" /></template>
      </t-button>
    </form>
  </section>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import type { WorkbenchApi } from '@/api/sandbox-workbench'
import { getApiBaseUrl } from '@/utils/api-base'
import { SandboxTerminal, type TerminalPhase } from '@/utils/sandboxTerminal'
import { TERMINAL_OUTPUT_BYTES, workbenchError } from '@/utils/sandboxWorkbench'

const props = defineProps<{ api: WorkbenchApi; signal: AbortSignal; active: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const terminalHost = ref<HTMLElement>()
const phase = ref<TerminalPhase>('disconnected')
const command = ref('')
const error = ref('')
const truncated = ref(false)
const lastExit = ref<{ exit_code: number; reason?: string }>()
let terminal: Terminal | undefined
let fit: FitAddon | undefined
let observer: ResizeObserver | undefined
let connection: SandboxTerminal | undefined
let frame = 0
let writtenBytes = 0
let disposed = false

function fitTerminal() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(() => {
    if (!disposed && terminalHost.value?.clientWidth && terminalHost.value?.clientHeight) fit?.fit()
  })
}

function clearOutput() {
  terminal?.reset()
  writtenBytes = 0
  truncated.value = false
}

function connect() {
  error.value = ''
  void connection?.connect()
}

function run() {
  if (connection?.command(command.value)) {
    command.value = ''
    lastExit.value = undefined
    error.value = ''
    terminal?.focus()
  }
}

function dispose() {
  if (disposed) return
  disposed = true
  props.signal.removeEventListener('abort', dispose)
  connection?.dispose()
  observer?.disconnect()
  cancelAnimationFrame(frame)
  terminal?.dispose()
}

onMounted(() => {
  if (props.signal.aborted || !terminalHost.value) return
  terminal = new Terminal({
    disableStdin: true, cursorBlink: false, scrollback: 3000,
    fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    theme: { background: '#17191c', foreground: '#e3e5e8', cursor: '#e3e5e8', selectionBackground: '#454a53' },
    allowProposedApi: false, convertEol: false,
  })
  fit = new FitAddon()
  terminal.loadAddon(fit)
  terminal.open(terminalHost.value)
  connection = new SandboxTerminal({
    apiBase: getApiBaseUrl(), pageUrl: window.location.href,
    ticket: props.api.ticket, signal: props.signal,
    onPhase: value => {
      phase.value = value
      if (terminal && !disposed) {
        terminal.options.disableStdin = value !== 'running'
        terminal.options.cursorBlink = value === 'running'
      }
    },
    onOutput: bytes => {
      const remaining = TERMINAL_OUTPUT_BYTES - writtenBytes
      if (remaining < bytes.byteLength) truncated.value = true
      if (remaining > 0) {
        const output = bytes.subarray(0, remaining)
        writtenBytes += output.byteLength
        terminal?.write(output)
      }
    },
    onEvent: event => {
      if (event.type === 'ready') {
        fitTerminal()
        if (terminal) connection?.resize(terminal.cols, terminal.rows)
      }
      if (event.type === 'started') terminal?.focus()
      if (event.type === 'exit') { lastExit.value = event; emit('changed') }
    },
    onError: value => { error.value = workbenchError(value) || t('workbench.requestFailed') },
  })
  terminal.onData(data => connection?.stdin(data))
  terminal.onBinary(data => connection?.stdin(data))
  terminal.onResize(({ cols, rows }) => connection?.resize(cols, rows))
  observer = new ResizeObserver(fitTerminal)
  observer.observe(terminalHost.value)
  props.signal.addEventListener('abort', dispose, { once: true })
  fitTerminal()
  connect()
})

watch(() => props.active, async active => { if (active) { await nextTick(); fitTerminal() } })
onBeforeUnmount(dispose)
</script>

<style scoped lang="less">
.workbench-terminal { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; }
.terminal-state { flex: 1; }
.terminal-host { flex: 1; min-height: 160px; min-width: 0; overflow: hidden; padding: 8px; background: #17191c; }
.terminal-result { min-height: 28px; padding: 6px 12px; font-size: 12px; color: var(--td-text-color-secondary); overflow-wrap: anywhere; }
.command-form { display: flex; align-items: flex-end; gap: 8px; padding: 0 12px 12px; }
.command-form :deep(.t-textarea) { flex: 1; min-width: 0; font-family: ui-monospace, monospace; }
</style>
