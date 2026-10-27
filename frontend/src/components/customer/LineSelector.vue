<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MorphIcon } from 'morphicons/vue'
import { ChevronDown, ChevronUp } from 'lucide'
import { brandConfig } from '@/brand'
import { resolveLine } from '@/brand/productLines'
import type { ProductLine } from '@/brand/types'

/**
 * 客户版产线选择器。
 *
 * 受控组件：当前值由 `modelValue` 传入、选择结果通过 `update:modelValue` 抛出，
 * 状态本身落在 productLine store 里。这里不读 settings store，
 * 也不碰智能体/知识库/文件/会话（产线切换本期只影响界面，AC-18 / C7）。
 *
 * 箭头用 morphicons 在 ChevronDown ↔ ChevronUp 之间形变（AC-28）；
 * 图标数据只从 lucide 具名导入，避免把整包图标打进产物（C8）。
 */
const props = withDefaults(
  defineProps<{
    /** 当前产线 id，通常绑定 productLine store 的 currentLineId */
    modelValue?: string
    /** 产线清单，默认取运行时品牌配置 */
    lines?: readonly ProductLine[]
    disabled?: boolean
  }>(),
  {
    modelValue: '',
    lines: () => brandConfig.productLines,
    disabled: false,
  },
)

const emit = defineEmits<{
  'update:modelValue': [lineId: string]
  change: [line: ProductLine]
}>()

const { t } = useI18n()

const rootRef = ref<HTMLElement>()
const triggerRef = ref<HTMLButtonElement>()
const open = ref(false)
const activeIndex = ref(0)

const currentLine = computed(() => resolveLine(props.lines, props.modelValue))
const activeLine = computed(() => props.lines[activeIndex.value])

function selectedIndex(): number {
  const index = props.lines.findIndex((line) => line.id === currentLine.value?.id)
  return index >= 0 ? index : 0
}

function openList(): void {
  if (props.disabled || open.value || props.lines.length === 0) return
  activeIndex.value = selectedIndex()
  open.value = true
}

function closeList(returnFocus = false): void {
  if (!open.value) return
  open.value = false
  if (returnFocus) {
    // Esc / 选择后用键盘的人应该回到触发按钮，而不是掉到页面顶部。
    nextTick(() => triggerRef.value?.focus())
  }
}

function toggleList(): void {
  if (open.value) closeList()
  else openList()
}

/** 方向键在列表里循环移动高亮项。 */
function moveActive(step: number): void {
  const count = props.lines.length
  if (count === 0) return
  activeIndex.value = (activeIndex.value + step + count) % count
}

function choose(index: number): void {
  const line = props.lines[index]
  if (!line) return
  emit('update:modelValue', line.id)
  emit('change', line)
  closeList(true)
}

function onTriggerKeydown(event: KeyboardEvent): void {
  switch (event.key) {
    case 'Enter':
    case ' ':
      event.preventDefault()
      if (open.value) choose(activeIndex.value)
      else openList()
      break
    case 'ArrowDown':
      event.preventDefault()
      if (open.value) moveActive(1)
      else openList()
      break
    case 'ArrowUp':
      event.preventDefault()
      if (open.value) moveActive(-1)
      else openList()
      break
    case 'Escape':
      if (open.value) {
        event.preventDefault()
        closeList()
      }
      break
  }
}

function onDocumentPointerDown(event: PointerEvent): void {
  if (!open.value) return
  const target = event.target as Node | null
  if (rootRef.value && target && !rootRef.value.contains(target)) closeList()
}

watch(open, (value) => {
  if (typeof document === 'undefined') return
  if (value) document.addEventListener('pointerdown', onDocumentPointerDown)
  else document.removeEventListener('pointerdown', onDocumentPointerDown)
})

// 外部（例如设置页）改了当前产线时，高亮项跟着走。
watch(
  () => props.modelValue,
  () => {
    activeIndex.value = selectedIndex()
  },
)

onBeforeUnmount(() => {
  if (typeof document !== 'undefined') document.removeEventListener('pointerdown', onDocumentPointerDown)
})
</script>

<template>
  <div ref="rootRef" class="line-selector">
    <button
      ref="triggerRef"
      type="button"
      class="line-selector__trigger"
      :disabled="disabled"
      aria-haspopup="listbox"
      :aria-expanded="open"
      aria-controls="customer-line-listbox"
      :aria-activedescendant="open && activeLine ? `customer-line-option-${activeLine.id}` : undefined"
      :aria-label="`${t('brand.lineSelector.currentLine')}: ${currentLine?.name ?? ''}`"
      :title="t('brand.lineSelector.changeLine')"
      @click="toggleList"
      @keydown="onTriggerKeydown"
    >
      <span class="line-selector__name">{{ currentLine?.name ?? t('brand.lineSelector.empty') }}</span>
      <!-- 纯装饰图标：名字和 aria-label 已在触发按钮上，MorphIcon 不传 label 会自动 aria-hidden -->
      <MorphIcon
        class="line-selector__chevron"
        :icon="open ? ChevronUp : ChevronDown"
        :size="14"
        :stroke-width="2"
        spring="snappy"
        reduced-motion="user"
      />
    </button>

    <!-- 展开/收起都走过渡：关闭时元素先留在 DOM 里淡出，不瞬间消失（AC-27） -->
    <Transition name="line-dropdown">
      <ul
        v-if="open"
        id="customer-line-listbox"
        class="line-selector__list"
        role="listbox"
        :aria-label="t('brand.lineSelector.listLabel')"
      >
        <li
          v-for="(line, index) in lines"
          :id="`customer-line-option-${line.id}`"
          :key="line.id"
          class="line-selector__option"
          :class="{ 'is-active': index === activeIndex, 'is-selected': line.id === currentLine?.id }"
          role="option"
          :aria-selected="line.id === currentLine?.id"
          @mouseenter="activeIndex = index"
          @click="choose(index)"
        >
          <span class="line-selector__option-main">
            <span class="line-selector__option-name">{{ line.name }}</span>
            <span v-if="line.description" class="line-selector__option-desc">{{ line.description }}</span>
          </span>
          <t-icon
            v-if="line.id === currentLine?.id"
            name="check"
            size="16px"
            class="line-selector__check"
            aria-hidden="true"
          />
        </li>
        <li v-if="lines.length === 0" class="line-selector__empty">{{ t('brand.lineSelector.empty') }}</li>
      </ul>
    </Transition>
  </div>
</template>

<style scoped lang="less">
.line-selector {
  position: relative;
  flex-shrink: 0;
}

.line-selector__trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-md);
  cursor: pointer;
  transition:
    border-color var(--app-motion-instant),
    background var(--app-motion-instant),
    color var(--app-motion-instant);

  &:hover:not(:disabled),
  &[aria-expanded='true'] {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 2px;
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
}

.line-selector__name {
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 形变由 morphicons 负责，这里不再用 rotate 过渡，避免形变和旋转叠加。
   尺寸走 MorphIcon 的 size 属性（14px），只保留布局相关的收缩。 */
.line-selector__chevron {
  flex-shrink: 0;
}

.line-selector__list {
  position: absolute;
  left: 0;
  bottom: calc(100% + 6px);
  z-index: 20;
  width: 280px;
  margin: 0;
  padding: 4px;
  list-style: none;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  box-shadow: var(--td-shadow-2);
}

.line-selector__option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-primary);
  cursor: pointer;

  &.is-active {
    background: var(--td-bg-color-secondarycontainer-hover);
  }

  &.is-selected {
    color: var(--td-brand-color);
  }
}

.line-selector__option-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.line-selector__option-name {
  font-size: var(--app-text-md);
  line-height: 20px;
}

.line-selector__option-desc {
  font-size: var(--app-text-sm);
  line-height: 18px;
  color: var(--td-text-color-secondary);
}

.line-selector__check {
  flex-shrink: 0;
  color: var(--td-brand-color);
}

.line-selector__empty {
  padding: 8px 10px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

/* 下拉展开/收起：淡入淡出 + 4px 位移，打开 150ms、收起 120ms（AC-27）。 */
.line-dropdown-enter-active {
  transition:
    opacity var(--app-motion-fast) ease-out,
    transform var(--app-motion-fast) ease-out;
}

.line-dropdown-leave-active {
  /* 收起过程中不再接收点击，避免过渡期里点到一个正在消失的选项 */
  pointer-events: none;
  transition:
    opacity var(--app-motion-instant) ease-in,
    transform var(--app-motion-instant) ease-in;
}

.line-dropdown-enter-from,
.line-dropdown-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

@media (prefers-reduced-motion: reduce) {
  .line-selector__trigger {
    transition: none;
  }

  .line-dropdown-enter-active,
  .line-dropdown-leave-active {
    transition: none;
  }

  .line-dropdown-enter-from,
  .line-dropdown-leave-to {
    opacity: 1;
    transform: none;
  }
}
</style>
