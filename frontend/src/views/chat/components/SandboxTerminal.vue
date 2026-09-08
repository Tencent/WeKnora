<template>
    <div ref="containerRef" class="sandbox-terminal" :class="{ 'is-dark': isDarkTheme }"
        @mousedown="focusTerminal">
        <div v-if="status !== 'ready'" class="sandbox-terminal__overlay">
            <div class="sandbox-terminal__overlay-card">
                <t-icon v-if="status === 'connecting'" name="loading" size="24px"
                    class="sandbox-terminal__spinner" />
                <t-icon
                    v-else-if="status === 'not_started' || status === 'needs_provision' || status === 'no_sandbox'"
                    name="terminal" size="28px" />
                <t-icon v-else-if="status === 'unsupported'" name="error-circle" size="28px" />
                <t-icon v-else-if="status === 'idle'" name="time" size="28px" />
                <t-icon v-else name="cloud" size="28px" />
                <p class="sandbox-terminal__overlay-text">{{ statusText }}</p>
                <t-button v-if="actionLabel" size="small"
                    :theme="status === 'needs_provision' ? 'primary' : 'default'" variant="outline"
                    @click.stop="start">
                    {{ actionLabel }}
                </t-button>
            </div>
        </div>
        <div ref="terminalHost" class="sandbox-terminal__host"></div>
    </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, toRef, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import { useSandboxTerminal, type SandboxTerminalStatus } from '@/composables/useSandboxTerminal';
import { useTheme } from '@/composables/useTheme';
import { createPtyEchoPredictor } from '@/utils/ptyEchoPredictor';

const props = defineProps<{
    sessionId: string;
    /** 当前会话选中的 agent：首次连接时后端按其配置自动创建沙箱。 */
    agentId?: string;
    /** 共享智能体的来源空间，与聊天请求的 agent_source_tenant_id 一致。 */
    agentSourceTenantId?: string | number | null;
}>();

const { t } = useI18n();

const containerRef = ref<HTMLElement | null>(null);
const terminalHost = ref<HTMLElement | null>(null);

// 主题必须从 useTheme 的共享 ref 派生，不能读 `theme-mode` DOM 属性：DOM 属性
// 不是响应式数据源，那样写出来的 computed 没有任何依赖，求值一次就永久缓存，
// 下面的 watch 永远不会触发，终端配色会一直停在首次挂载时的样子。
const { currentTheme } = useTheme();
const systemPrefersDark = ref(prefersDarkQuery()?.matches === true);
const isDarkTheme = computed(() =>
    currentTheme.value === 'system' ? systemPrefersDark.value : currentTheme.value === 'dark',
);

function prefersDarkQuery(): MediaQueryList | null {
    if (typeof window === 'undefined' || !window.matchMedia) return null;
    return window.matchMedia('(prefers-color-scheme: dark)');
}

// `system` 模式下还要跟随操作系统的实时切换；useTheme 的全局监听只写 DOM
// 属性，不暴露生效后的明暗值，所以这里自己订阅一份。
const prefersDark = prefersDarkQuery();
const onSystemThemeChange = (event: MediaQueryListEvent) => {
    systemPrefersDark.value = event.matches;
};
prefersDark?.addEventListener('change', onSystemThemeChange);

function xtermTheme(dark: boolean) {
    return dark
        ? {
            background: '#1a1a1a',
            foreground: '#e6e6e6',
            cursor: '#e6e6e6',
            cursorAccent: '#1a1a1a',
            selectionBackground: '#3a3a3a',
        }
        : {
            background: '#ffffff',
            foreground: '#242424',
            cursor: '#242424',
            cursorAccent: '#ffffff',
            selectionBackground: '#d0d7de',
        };
}

// toRef 而非 ref(props.x)：后者是快照，切换 agent 后重连仍会沿用旧 agent 的
// 沙箱配置去创建沙箱。sessionId 由父组件的 :key 重建兜住，agentId 不会。
const terminal = useSandboxTerminal(
    toRef(props, 'sessionId'),
    toRef(props, 'agentId'),
    toRef(props, 'agentSourceTenantId'),
);
const { status } = terminal;

const statusText = computed(() => {
    switch (status.value as SandboxTerminalStatus) {
        case 'not_started':
            return t('chat.sandbox.notStarted');
        case 'connecting':
            return t('chat.sandbox.connecting');
        case 'needs_provision':
            return t('chat.sandbox.needsProvision');
        case 'no_sandbox':
            return t('chat.sandbox.noSandbox');
        case 'unsupported':
            return t('chat.sandbox.unsupported');
        case 'exited':
            return t('chat.sandbox.sessionEnded');
        case 'idle':
            return t('chat.sandbox.idleDisconnected');
        case 'unauthorized':
            return t('chat.sandbox.authRevoked');
        default:
            return t('chat.sandbox.disconnected');
    }
});

// 覆盖层上那颗按钮的文案；空串表示这个状态没有可执行的动作。
// 每次点击都视为"用户确认"，因此允许后端创建或唤醒沙箱——面板本身永远不会。
const actionLabel = computed(() => {
    switch (status.value as SandboxTerminalStatus) {
        case 'not_started':
            return t('chat.sandbox.start');
        case 'needs_provision':
            return t('chat.sandbox.createAndStart');
        case 'error':
        case 'exited':
        case 'idle':
        case 'unauthorized':
        // no_sandbox 说的是"智能体没配沙箱后端，无处可建"。用户去配好后应该能
        // 直接重试，而不是只能关掉面板再打开——那是个没有任何操作入口的死角。
        case 'no_sandbox':
            return t('chat.sandbox.retry');
        default:
            return '';
    }
});

// xterm 实例只在 ready 后挂载一次；overlay 覆盖在其上展示状态。
let xterm: Terminal | null = null;
let fitAddon: FitAddon | null = null;
let echo: ReturnType<typeof createPtyEchoPredictor> | null = null;
let resizeObserver: ResizeObserver | null = null;
let resizeDebounce: ReturnType<typeof setTimeout> | null = null;
let unmounted = false;

function writeToXterm(chunk: string | Uint8Array) {
    xterm?.write(chunk);
}

function focusTerminal() {
    if (status.value !== 'ready') return;
    xterm?.focus();
}

function mountTerminal() {
    if (!terminalHost.value || xterm) return;
    xterm = new Terminal({
        cursorBlink: true,
        cursorStyle: 'bar',
        cursorInactiveStyle: 'outline',
        fontSize: 13,
        fontFamily: "'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace",
        theme: xtermTheme(isDarkTheme.value),
        scrollback: 5000,
    });
    echo = createPtyEchoPredictor(writeToXterm);
    fitAddon = new FitAddon();
    xterm.loadAddon(fitAddon);
    xterm.open(terminalHost.value);
    xterm.onData((data) => {
        echo?.onLocal(data);
        terminal.sendInput(data);
    });
    // 必须在 connect 之后、但在有输出要展示时注册；composable 会先冲出缓冲的提示符。
    terminal.onOutput((data) => echo?.onRemote(data));

    resizeObserver = new ResizeObserver(() => {
        if (resizeDebounce) clearTimeout(resizeDebounce);
        resizeDebounce = setTimeout(() => {
            if (!fitAddon || !xterm) return;
            try {
                fitAddon.fit();
                terminal.resize(xterm.cols, xterm.rows);
            } catch {
                // fit 在容器尺寸为 0 时会抛错，忽略即可。
            }
        }, 100);
    });
    resizeObserver.observe(containerRef.value || terminalHost.value);
    try {
        fitAddon.fit();
    } catch {
        // 首帧布局未就绪时忽略。
    }
    terminal.resize(xterm.cols, xterm.rows);
    void nextTick(() => {
        requestAnimationFrame(() => fitAndFocus());
    });
}

function unmountTerminal() {
    terminal.onOutput(null);
    echo = null;
    resizeObserver?.disconnect();
    resizeObserver = null;
    if (resizeDebounce) clearTimeout(resizeDebounce);
    resizeDebounce = null;
    xterm?.dispose();
    xterm = null;
    fitAddon = null;
}

// 覆盖层上唯一的连接入口。provision: true 表示这是用户的显式确认，允许后端
// 创建或唤醒沙箱；组件挂载本身不连接，所以打开面板不会产生任何计费副作用。
function start() {
    unmountTerminal();
    terminal.connect({ provision: true });
}

watch(isDarkTheme, (dark) => {
    if (xterm) xterm.options.theme = xtermTheme(dark);
});

// ready 后挂载 xterm；离开非 ready 状态重置实例（重连 = 新 PTY）。
watch(status, (next, prev) => {
    if (next === 'ready' && prev !== 'ready') {
        requestAnimationFrame(() => {
            // 组件可能在 ready 与下一帧之间就被卸载（关面板 / 切会话）。不校验
            // 就会在已销毁的组件上新建 Terminal 并 observe 一个游离节点，那个
            // 实例连 watch 都救不回来。
            if (unmounted) return;
            mountTerminal();
        });
    } else if (prev === 'ready' && next !== 'ready') {
        unmountTerminal();
    }
});

// watch(status) 只覆盖"状态离开 ready"，覆盖不到组件本身被销毁：关面板走
// SandboxSidePanel 的 v-if、切会话走 :key 重建，两条路径下 status 全程不变，
// watch 不触发。少了这个钩子，每次开关面板都会漏掉一个 xterm 实例
// （渲染器 + 5000 行 scrollback）和一个 ResizeObserver。
onBeforeUnmount(() => {
    unmounted = true;
    prefersDark?.removeEventListener('change', onSystemThemeChange);
    unmountTerminal();
});

function fitAndFocus() {
    try {
        fitAddon?.fit();
    } catch {
        // 容器尚未完成布局时忽略。
    }
    if (xterm) terminal.resize(xterm.cols, xterm.rows);
    xterm?.focus();
}

defineExpose({
    focus: fitAndFocus,
});
</script>

<style scoped lang="less">
.sandbox-terminal {
    position: relative;
    height: 100%;
    min-height: 0;
    display: flex;
    flex-direction: column;
    background: var(--td-bg-color-container);
    border-radius: 8px;
    overflow: hidden;
    cursor: text;
}

.sandbox-terminal__host {
    flex: 1;
    min-height: 0;
    padding: 8px;
    cursor: text;

    :deep(.xterm) {
        height: 100%;
        cursor: text;
    }

    :deep(.xterm-viewport) {
        overflow-y: auto;
    }

    :deep(.xterm-helper-textarea) {
        // xterm 用隐藏 textarea 接收键盘；必须能获得焦点光标才会闪。
        pointer-events: auto;
    }
}

.sandbox-terminal__overlay {
    position: absolute;
    inset: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--td-bg-color-container);
    cursor: default;
}

.sandbox-terminal__overlay-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    max-width: 280px;
    padding: 20px;
    text-align: center;
    color: var(--td-text-color-placeholder);
}

.sandbox-terminal__overlay-text {
    margin: 0;
    font-size: 13px;
    line-height: 1.6;
    white-space: pre-line;
}

.sandbox-terminal__spinner {
    animation: sandbox-terminal-spin 0.9s linear infinite;
}

@keyframes sandbox-terminal-spin {
    from {
        transform: rotate(0deg);
    }

    to {
        transform: rotate(360deg);
    }
}
</style>
