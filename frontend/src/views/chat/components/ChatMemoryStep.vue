<template>
  <div
    class="chat-memory-step"
    :class="variant === 'root'
      ? 'memory-root'
      : ['tree-child', 'memory-step', { 'tree-child-last': isLast }]"
  >
    <div
      class="memory-header"
      role="button"
      tabindex="0"
      :aria-expanded="expanded"
      @click="emit('toggle')"
      @keydown.enter.prevent="emit('toggle')"
      @keydown.space.prevent="emit('toggle')"
    >
      <t-icon v-if="variant !== 'root'" class="memory-icon" name="bookmark" />
      <span class="memory-name">{{ t('chat.memoryUsedCount', { count: memories.length }) }}</span>
      <t-icon class="memory-chevron" :name="expanded ? 'chevron-down' : 'chevron-right'" />
    </div>

    <div v-if="expanded" class="memory-detail-content">
      <div v-for="memory in memories" :key="memory.id" class="memory-row">
        <span class="memory-kind">{{ memoryKindLabel(memory.kind) }}</span>
        <span class="memory-text">{{ memory.content }}</span>
        <button
          type="button"
          class="memory-forget"
          :disabled="forgettingId === memory.id"
          :title="t('chat.memoryForget')"
          @click.stop="emit('forget', memory)"
        >
          <t-icon name="delete" />
        </button>
      </div>
      <p class="memory-hint">{{ t('chat.memoryHint') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

type Memory = { id: string; kind: string; content: string }

withDefaults(
  defineProps<{
    memories: Memory[]
    expanded?: boolean
    // A step sits inside an existing timeline; a root leads a turn that has no
    // timeline of its own, so it lines up with the collapsed headers next to it
    // instead of pretending to be a branch of something.
    variant?: 'step' | 'root'
    isLast?: boolean
    forgettingId?: string
  }>(),
  { variant: 'step' },
)

const emit = defineEmits<{
  (event: 'toggle'): void
  (event: 'forget', memory: Memory): void
}>()

const { t } = useI18n()

const MEMORY_KINDS = ['profile', 'preference', 'fact', 'task', 'interest'] as const

const memoryKindLabel = (kind: string) => {
  if ((MEMORY_KINDS as readonly string[]).includes(kind)) {
    return t(`memorySettings.kinds.${kind}`)
  }
  return t('memorySettings.kinds.fact')
}
</script>

<style scoped lang="less">
// Layout for the step variant comes from the host timeline's .tree-child rules,
// which reach this component through its root element. Everything inside it has
// to be styled here.
.memory-header {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 24px;
  cursor: pointer;
  user-select: none;

  &:focus-visible {
    outline: none;

    .memory-name {
      color: var(--td-text-color-primary);
    }
  }
}

.memory-icon {
  position: absolute;
  left: -42px;
  top: 3px;
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  color: var(--agent-step-icon-color, var(--td-text-color-placeholder));
}

.memory-name {
  font-size: var(--agent-step-text-size, 14px);
  line-height: 1.55;
  font-weight: 400;
  color: var(--td-text-color-secondary);
  word-break: break-word;
}

.memory-chevron {
  flex-shrink: 0;
  font-size: 13px;
  color: var(--agent-step-icon-color, var(--td-text-color-placeholder));
}

.memory-root {
  margin: 0;

  .memory-header {
    gap: 6px;
    min-height: 22px;
    line-height: 22px;

    &:hover .memory-name {
      color: var(--td-text-color-primary);
    }
  }

  .memory-chevron {
    font-size: 14px;
    color: currentColor;
  }
}

.memory-detail-content {
  width: 100%;
  margin-top: 4px;
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.memory-row {
  display: flex;
  width: 100%;
  align-items: baseline;
  gap: 8px;
  padding: 2px 0;
}

// A plain label would run straight into the sentence ("个人信息在做医疗影像
// …"), so the kind reads as a tag rather than as the first words of the memory.
.memory-kind {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: 3px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 18px;
}

.memory-text {
  flex: 1 1 auto;
  min-width: 0;
  word-break: break-word;
}

.memory-forget {
  flex: 0 0 auto;
  margin-left: auto;
  padding: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, color 0.15s ease;

  &:focus-visible {
    opacity: 1;
    color: var(--td-error-color);
  }

  &:hover:not(:disabled) {
    color: var(--td-error-color);
  }

  &:disabled {
    cursor: default;
    opacity: 0.5;
  }
}

// Revealing the control on hover keeps the list readable as a list, while
// still putting delete one click away from the memory it belongs to.
.memory-row:hover .memory-forget {
  opacity: 1;
}

.memory-hint {
  margin: 6px 0 0;
  color: var(--td-text-color-placeholder);
}
</style>
