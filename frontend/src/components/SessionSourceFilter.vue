<template>
  <div class="session-source-filter" :class="{
    'session-source-filter--inline': inline,
    'session-source-filter--emphasized': emphasized,
  }">
    <button ref="triggerRef" type="button" class="session-source-filter__trigger" :aria-expanded="open"
      aria-haspopup="listbox" @click.stop="toggleOpen">
      <span class="session-source-filter__leading">
        <img v-if="!inline && currentOption?.logo" :src="currentOption.logo" :alt="currentOption.label"
          class="session-source-filter__logo" />
        <t-icon v-else-if="!inline" :name="iconFor(currentOption)" class="session-source-filter__icon" size="14px" />
        <span class="session-source-filter__label" :title="currentOption?.label">{{ currentOption?.label }}</span>
      </span>
      <t-icon v-if="inline" name="chevron-down" class="session-source-filter__chevron"
        :class="{ 'session-source-filter__chevron--open': open }" size="10px" />
      <t-icon v-else name="chevron-down" class="session-source-filter__chevron"
        :class="{ 'session-source-filter__chevron--open': open }" size="12px" />
    </button>
    <Teleport to="body">
      <div v-if="open" class="session-source-filter__panel" role="listbox" :style="panelStyle" @click.stop>
        <button v-for="item in sources" :key="item.value" type="button" class="session-source-filter__option"
          :class="{ 'session-source-filter__option--active': item.value === current }" role="option"
          :aria-selected="item.value === current" @click="handleSelect(item.value)">
          <span class="session-source-filter__option-leading">
            <img v-if="item.logo" :src="item.logo" :alt="item.label" class="session-source-filter__logo" />
            <t-icon v-else :name="iconFor(item)" class="session-source-filter__icon" size="14px" />
            <span class="session-source-filter__option-label" :title="item.label">{{ item.label }}</span>
          </span>
          <t-icon v-if="item.value === current" name="check" class="session-source-filter__check" size="13px" />
        </button>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { DEFAULT_SESSION_BUCKET_KEY } from './sessionSidebarSourceFilter'

interface SourceItem {
  value: string
  label: string
  logo?: string
}

const props = defineProps<{
  sources: SourceItem[]
  current: string
  /** 列表顶部的轻量文字触发器（无图标，右对齐） */
  inline?: boolean
  /** 非默认来源时始终显示（便于切回网页对话） */
  emphasized?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', value: string): void
}>()

const PANEL_GAP = 4
const VIEWPORT_MARGIN = 8

const open = ref(false)
const triggerRef = ref<HTMLButtonElement | null>(null)
const panelStyle = ref<Record<string, string>>({})

const currentOption = computed(() =>
  props.sources.find((item) => item.value === props.current) ?? props.sources[0],
)

const iconFor = (item: SourceItem | undefined): string => {
  if (!item) return 'chat'
  if (item.value === DEFAULT_SESSION_BUCKET_KEY) return 'chat'
  if (item.value.startsWith('embed:')) return 'internet'
  return 'link'
}

const updatePanelPosition = (): void => {
  const trigger = triggerRef.value
  if (!trigger) return
  const rect = trigger.getBoundingClientRect()
  const width = Math.max(rect.width, 168)
  const left = props.inline
    ? Math.max(VIEWPORT_MARGIN, rect.right - width)
    : Math.max(
      VIEWPORT_MARGIN,
      Math.min(rect.left, window.innerWidth - width - VIEWPORT_MARGIN),
    )
  panelStyle.value = {
    top: `${rect.bottom + PANEL_GAP}px`,
    left: `${left}px`,
    width: `${width}px`,
  }
}

const removeListeners = (): void => {
  document.removeEventListener('click', close)
  window.removeEventListener('resize', close)
  window.removeEventListener('scroll', close, true)
}

const close = (): void => {
  open.value = false
  removeListeners()
}

const toggleOpen = (): void => {
  if (open.value) {
    close()
    return
  }
  updatePanelPosition()
  open.value = true
  nextTick(() => {
    document.addEventListener('click', close)
    window.addEventListener('resize', close)
    window.addEventListener('scroll', close, true)
  })
}

const handleSelect = (value: string): void => {
  close()
  if (value === props.current) return
  emit('select', value)
}

onBeforeUnmount(() => {
  removeListeners()
})
</script>

<style scoped lang="less">
.session-source-filter {
  padding: 2px 0 6px;

  &--inline {
    padding: 0;
    min-width: 0;
    max-width: 100%;

    .session-source-filter__leading {
      gap: 0;
      flex: 0 1 auto;
    }
  }
}

.session-source-filter__trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-height: 28px;
  padding: 4px 10px 4px 14px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
  font-family: var(--app-font-family);
  text-align: left;

  &:hover,
  &[aria-expanded='true'] {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  .session-source-filter--inline & {
    width: auto;
    max-width: 100%;
    min-height: 0;
    gap: 2px;
    padding: 0;
    border-radius: 0;
    color: var(--td-text-color-disabled);
    justify-content: flex-end;

    &:hover,
    &[aria-expanded='true'] {
      background: transparent;
      color: var(--td-text-color-placeholder);
    }
  }
}

.session-source-filter__leading,
.session-source-filter__option-leading {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex: 1 1 auto;
}

.session-source-filter__label,
.session-source-filter__option-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
  letter-spacing: 0.01em;

  .session-source-filter--inline .session-source-filter__trigger & {
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: inherit;
  }
}

.session-source-filter__logo {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
  object-fit: contain;
  opacity: 0.72;
  filter: grayscale(0.15);

  .session-source-filter--inline & {
    width: 12px;
    height: 12px;
    opacity: 0.55;
  }
}

.session-source-filter__icon {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);

  .session-source-filter--inline & {
    font-size: 12px !important;
    color: var(--td-text-color-disabled);
  }
}

.session-source-filter__chevron {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);
  transition: transform 0.18s ease, color 0.15s ease;

  &--open {
    transform: rotate(180deg);
    color: var(--td-text-color-secondary);
  }

  .session-source-filter--inline & {
    color: var(--td-text-color-disabled);
    opacity: 0.85;
    font-size: 10px !important;
  }
}

.session-source-filter__panel {
  position: fixed;
  z-index: 3000;
  padding: 4px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-sidebar, var(--td-bg-color-container));
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.06);
}

.session-source-filter__option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-height: 30px;
  padding: 5px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
  font-family: var(--app-font-family);
  text-align: left;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &--active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);

    .session-source-filter__icon {
      color: var(--td-text-color-secondary);
    }

    .session-source-filter__logo {
      opacity: 0.9;
      filter: none;
    }
  }
}

.session-source-filter__option-label {
  font-size: 13px;
  font-weight: 430;
  line-height: 18px;
}

.session-source-filter__check {
  flex: 0 0 auto;
  color: var(--td-text-color-secondary);
}
</style>
