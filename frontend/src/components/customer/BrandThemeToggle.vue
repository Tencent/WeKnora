<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MorphIcon } from 'morphicons/vue'
import { Moon, Sun } from 'lucide'
import type { ThemeMode } from '@/composables/useTheme'

/**
 * 客户版主题切换（基础版）。
 *
 * 受控组件：当前值由 `modelValue` 传入，选择结果通过 `update:modelValue` / `change` 抛出。
 * 持久化统一交给 `useTheme().setTheme`，这里不自己写 localStorage —— 主题偏好按用户
 * 命名空间存放（preferenceStorage），另建一套会和管理员端各存各的。
 *
 * 三态与 `useTheme` 支持的模式一致（light / dark / system），沿用现有 theme.* 文案。
 *
 * 图标：控件左侧放一枚状态图标。浅色/深色之间切换时用 morphicons 的
 * Sun ↔ Moon 形变；跟随系统用静态显示器图标，不参与形变（T09 派单口径）。
 * 之所以放在控件左侧而不是每个选项里：三个选项同时存在时，把同一枚图标拆到
 * 两个按钮上无法做「同一个形状原地变形」，只会变成两个图标互相替换。
 * 选项本身仍保留可见文字，图标纯装饰并 aria-hidden。
 */
const props = defineProps<{
  modelValue: ThemeMode
}>()

const emit = defineEmits<{
  'update:modelValue': [mode: ThemeMode]
  change: [mode: ThemeMode]
}>()

const { t } = useI18n()

const rootRef = ref<HTMLElement>()

const options = computed<Array<{ value: ThemeMode; label: string }>>(() => [
  { value: 'light', label: t('theme.light') },
  { value: 'dark', label: t('theme.dark') },
  { value: 'system', label: t('theme.system') },
])

// 浅色 → Sun，深色 → Moon；同一个 MorphIcon 实例在两者之间做形变。
// 跟随系统时不渲染 MorphIcon（换成静态显示器 SVG），所以系统 ↔ 浅/深是直接切换。
const statusIcon = computed(() => (props.modelValue === 'dark' ? Moon : Sun))
const usesMorphStatus = computed(() => props.modelValue !== 'system')

function select(mode: ThemeMode): void {
  if (mode === props.modelValue) return
  emit('update:modelValue', mode)
  emit('change', mode)
}

/**
 * 单选组键盘约定：左右/上下移动并立即选中，Home/End 跳到首尾。
 * 用 roving tabindex（只有选中项 tabindex=0），Tab 进出整组只占一个停留点。
 */
function onKeydown(event: KeyboardEvent): void {
  const keys = options.value.map((option) => option.value)
  const current = keys.indexOf(props.modelValue)
  let next = -1

  switch (event.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      next = (current + 1) % keys.length
      break
    case 'ArrowLeft':
    case 'ArrowUp':
      next = (current - 1 + keys.length) % keys.length
      break
    case 'Home':
      next = 0
      break
    case 'End':
      next = keys.length - 1
      break
    default:
      return
  }

  event.preventDefault()
  const mode = keys[next]
  if (!mode) return
  select(mode)
  // 选中后焦点跟到新选项，键盘用户可以继续用方向键。
  rootRef.value
    ?.querySelectorAll<HTMLButtonElement>('[role="radio"]')
    [next]?.focus()
}
</script>

<template>
  <div
    ref="rootRef"
    class="theme-toggle"
    role="radiogroup"
    :aria-label="t('theme.selectTheme')"
    @keydown="onKeydown"
  >
    <!-- 状态图标：浅/深走 Sun ↔ Moon 形变；跟随系统换成静态显示器，不形变 -->
    <span class="theme-toggle__status" aria-hidden="true">
      <MorphIcon
        v-if="usesMorphStatus"
        :icon="statusIcon"
        :size="16"
        :stroke-width="2"
        spring="smooth"
        reduced-motion="user"
      />
      <svg
        v-else
        class="theme-toggle__icon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <rect x="2" y="3" width="20" height="14" rx="2" />
        <path d="M8 21h8M12 17v4" />
      </svg>
    </span>
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      class="theme-toggle__option"
      :class="{ 'is-checked': modelValue === option.value }"
      role="radio"
      :aria-checked="modelValue === option.value"
      :tabindex="modelValue === option.value ? 0 : -1"
      @click="select(option.value)"
    >
      <span class="theme-toggle__label">{{ option.label }}</span>
    </button>
  </div>
</template>

<style scoped lang="less">
.theme-toggle {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

/* 状态图标在三个选项左侧，不占按钮焦点序列，纯视觉提示 */
.theme-toggle__status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  margin: 0 2px 0 6px;
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.theme-toggle__option {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 28px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-md);
  cursor: pointer;
  transition:
    background var(--app-motion-instant),
    color var(--app-motion-instant);

  &:hover {
    color: var(--td-text-color-primary);
  }

  &.is-checked {
    background: var(--td-bg-color-container);
    color: var(--td-brand-color);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}

.theme-toggle__icon {
  width: 15px;
  height: 15px;
  flex-shrink: 0;
}

.theme-toggle__label {
  white-space: nowrap;
}

@media (prefers-reduced-motion: reduce) {
  .theme-toggle__option {
    transition: none;
  }
}
</style>
