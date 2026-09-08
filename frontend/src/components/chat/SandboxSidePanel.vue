<template>
  <Transition name="sandbox-panel">
    <aside
      v-if="panel?.visible.value"
      class="chat-sandbox-panel"
      :class="{ 'is-shifted': shifted, 'is-resizing': resizing }"
      :style="{ width: `${panel?.width.value ?? 420}px` }"
      role="complementary"
      :aria-label="t('chat.sandbox.panelTitle')"
    >
      <!-- 左缘拖拽把手：按住向左/右拖动调整面板宽度。 -->
      <div
        class="chat-sandbox-panel__resize-handle"
        :aria-hidden="true"
        @mousedown.prevent="startResize"
      />
      <header class="chat-sandbox-panel__header">
        <h3 class="chat-sandbox-panel__title">{{ t('chat.sandbox.panelTitle') }}</h3>
        <button
          type="button"
          class="chat-sandbox-panel__close"
          :aria-label="t('common.close')"
          @click="panel?.close()"
        >
          <t-icon name="close" size="20px" />
        </button>
      </header>

      <div class="chat-sandbox-panel__tabs" role="tablist">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          class="chat-sandbox-panel__tab"
          :class="{ 'is-active': panel?.activeTab.value === tab.id }"
          role="tab"
          :aria-selected="panel?.activeTab.value === tab.id"
          @click="panel?.open(tab.id)"
        >
          <t-icon :name="tab.icon" size="16px" />
          <span>{{ tab.label }}</span>
        </button>
      </div>

      <div class="chat-sandbox-panel__body">
        <!-- 终端：首次激活时惰性挂载；切 tab 用 v-show 保留实例（不丢 PTY）。 -->
        <SandboxTerminal
          v-if="terminalMounted"
          v-show="panel?.activeTab.value === 'terminal'"
          :key="sessionId"
          ref="terminalRef"
          :session-id="sessionId"
          :agent-id="agentId"
          class="chat-sandbox-panel__terminal"
        />
        <div v-else-if="panel?.activeTab.value === 'terminal'" class="chat-sandbox-panel__placeholder">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '100%', type: 'rect' }]" />
        </div>

        <div v-if="panel?.activeTab.value === 'desktop'" class="chat-sandbox-panel__placeholder">
          <t-icon name="desktop" size="28px" />
          <p>{{ t('chat.sandbox.desktopPlaceholder') }}</p>
        </div>
      </div>
    </aside>
  </Transition>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  useChatSandboxPanel,
  SANDBOX_PANEL_MIN_WIDTH,
  SANDBOX_PANEL_MAX_WIDTH,
  type SandboxPanelTab,
} from '@/composables/useChatSandboxPanel'
import SandboxTerminal from '@/views/chat/components/SandboxTerminal.vue'

const props = defineProps<{
  sessionId: string
  /** 当前会话选中的 agent（首次连接时按其配置自动创建沙箱）。 */
  agentId?: string
  /** 参考来源面板同开时整体左移，避免两块 fixed 面板重叠。 */
  shifted?: boolean
}>()

const { t } = useI18n()
const panel = useChatSandboxPanel()

const tabs = computed(() => [
  { id: 'terminal' as SandboxPanelTab, icon: 'terminal', label: t('chat.sandbox.tabTerminal') },
  { id: 'desktop' as SandboxPanelTab, icon: 'desktop', label: t('chat.sandbox.tabDesktop') },
])

// 终端实例惰性挂载（首次切到终端 tab 时），面板关闭即销毁（v-if），
// 与 ChatReferencesDrawer 的开合行为一致；会话切换时由 :key 重建。
const terminalMounted = ref(false)
const terminalRef = ref<{ focus?: () => void } | null>(null)

watch(
  () => [panel?.visible.value, panel?.activeTab.value] as const,
  ([visible, tab]) => {
    if (visible && tab === 'terminal') {
      terminalMounted.value = true
      void nextTick(() => terminalRef.value?.focus?.())
    }
  },
  { immediate: true },
)

// --- 左缘拖拽调宽 -------------------------------------------------------
const resizing = ref(false)
// 与面板样式一致：仅宽视口（≥1400px）且参考面板同开时才整体左移 420px。
const dragBaseOffset = () =>
  props.shifted && typeof window !== 'undefined' && window.innerWidth >= 1400 ? 420 : 0

function startResize(event: MouseEvent) {
  if (!panel) return
  resizing.value = true
  // 拖拽期间禁用全局文本选择，避免 mousemove 命中 iframe / 文本。
  document.body.style.userSelect = 'none'
  document.body.style.cursor = 'col-resize'

  const onMove = (moveEvent: MouseEvent) => {
    const next = window.innerWidth - moveEvent.clientX - dragBaseOffset()
    panel.setWidth(Math.min(SANDBOX_PANEL_MAX_WIDTH, Math.max(SANDBOX_PANEL_MIN_WIDTH, next)))
  }
  const cleanup = () => {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', cleanup)
    document.body.style.userSelect = ''
    document.body.style.cursor = ''
    resizing.value = false
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', cleanup)
}
</script>

<style scoped lang="less">
.chat-sandbox-panel {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(420px, 100vw);
  max-width: 100vw;
  z-index: 1201;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border-left: 1px solid var(--td-component-stroke);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.06);

  &.is-shifted {
    @media (min-width: 1400px) {
      right: 420px;
    }
  }

  // 拖拽调宽期间关闭过渡与文本选择，保证跟手。
  &.is-resizing {
    transition: none;
    user-select: none;

    .chat-sandbox-panel__body,
    .chat-sandbox-panel__tabs {
      pointer-events: none;
    }
  }
}

// 左缘拖拽把手：一条贴边的窄热区，hover 时显示视觉提示。
.chat-sandbox-panel__resize-handle {
  position: absolute;
  top: 0;
  left: -3px;
  bottom: 0;
  width: 7px;
  z-index: 3;
  cursor: col-resize;

  &::after {
    content: '';
    position: absolute;
    top: 0;
    left: 3px;
    bottom: 0;
    width: 1px;
    background: transparent;
    transition: background-color 0.15s ease;
  }

  &:hover::after,
  .is-resizing &::after {
    background: var(--td-brand-color);
  }
}

.chat-sandbox-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 16px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.chat-sandbox-panel__title {
  margin: 0;
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  line-height: 1.4;
}

.chat-sandbox-panel__close {
  border: 0;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  width: 32px;
  height: 32px;
  border-radius: 8px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: background 0.15s ease, color 0.15s ease;

  &:hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 8%, var(--td-bg-color-secondarycontainer));
    color: var(--td-text-color-primary);
  }
}

.chat-sandbox-panel__tabs {
  display: flex;
  gap: 4px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.chat-sandbox-panel__tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.chat-sandbox-panel__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 8px;
}

.chat-sandbox-panel__terminal {
  flex: 1;
  min-height: 0;
}

.chat-sandbox-panel__placeholder {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;

  p {
    margin: 0;
  }
}

.sandbox-panel-enter-active {
  transition:
    transform 0.24s cubic-bezier(0.22, 0.61, 0.36, 1),
    opacity 0.24s cubic-bezier(0.22, 0.61, 0.36, 1);
}

.sandbox-panel-leave-active {
  transition:
    transform 0.3s cubic-bezier(0.22, 0.61, 0.36, 1),
    opacity 0.3s cubic-bezier(0.22, 0.61, 0.36, 1);
}

.sandbox-panel-enter-from,
.sandbox-panel-leave-to {
  transform: translateX(100%);
  opacity: 0.6;
}
</style>
