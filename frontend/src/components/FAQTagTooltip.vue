<template>
  <div 
    ref="wrapperRef"
    class="faq-tag-wrapper"
    @mouseenter="handleMouseEnter"
    @mouseleave="handleMouseLeave"
  >
    <slot />
    <Teleport to="body">
      <Transition name="fade">
        <div
          v-if="showTooltip && content"
          ref="tooltipRef"
          class="faq-tag-tooltip"
          :class="tooltipClass"
          :style="tooltipStyle"
        >
          <div class="tooltip-content">{{ content }}</div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'

const props = defineProps<{
  content: string
  placement?: 'top' | 'bottom' | 'left' | 'right'
  type?: 'answer' | 'similar' | 'negative'
}>()

const showTooltip = ref(false)
const tooltipRef = ref<HTMLElement | null>(null)
const wrapperRef = ref<HTMLElement | null>(null)
const tooltipStyle = ref<{ top: string; left: string }>({ top: '0px', left: '0px' })

const tooltipClass = computed(() => {
  return {
    [`tooltip-${props.type || 'answer'}`]: true,
    [`placement-${props.placement || 'top'}`]: true,
  }
})

const updatePosition = async () => {
  if (!wrapperRef.value || !tooltipRef.value) return
  
  await nextTick()
  
  // 再次检查，确保DOM已渲染
  if (!tooltipRef.value) return
  
  const rect = wrapperRef.value.getBoundingClientRect()
  const tooltipRect = tooltipRef.value.getBoundingClientRect()
  const placement = props.placement || 'top'
  
  let top = 0
  let left = 0
  
  switch (placement) {
    case 'top':
      top = rect.top - tooltipRect.height - 8
      left = rect.left + (rect.width / 2) - (tooltipRect.width / 2)
      break
    case 'bottom':
      top = rect.bottom + 8
      left = rect.left + (rect.width / 2) - (tooltipRect.width / 2)
      break
    case 'left':
      top = rect.top + (rect.height / 2) - (tooltipRect.height / 2)
      left = rect.left - tooltipRect.width - 8
      break
    case 'right':
      top = rect.top + (rect.height / 2) - (tooltipRect.height / 2)
      left = rect.right + 8
      break
  }
  
  // 边界检测
  const padding = 8
  if (left < padding) left = padding
  if (left + tooltipRect.width > window.innerWidth - padding) {
    left = window.innerWidth - tooltipRect.width - padding
  }
  if (top < padding) {
    // 如果上方空间不足，改为下方显示
    if (placement === 'top') {
      top = rect.bottom + 8
    } else {
      top = padding
    }
  }
  if (top + tooltipRect.height > window.innerHeight - padding) {
    top = window.innerHeight - tooltipRect.height - padding
  }
  
  tooltipStyle.value = {
    top: `${top}px`,
    left: `${left}px`,
  }
}

const handleMouseEnter = () => {
  showTooltip.value = true
  nextTick(() => {
    updatePosition()
  })
}

const handleMouseLeave = () => {
  showTooltip.value = false
}

onMounted(() => {
  window.addEventListener('scroll', updatePosition, true)
  window.addEventListener('resize', updatePosition)
})

onUnmounted(() => {
  window.removeEventListener('scroll', updatePosition, true)
  window.removeEventListener('resize', updatePosition)
})

watch(showTooltip, (newVal) => {
  if (newVal) {
    nextTick(() => {
      updatePosition()
    })
  }
})
</script>

<style scoped lang="less">
.faq-tag-wrapper {
  display: inline-block;
  position: relative;
  max-width: 100%;
  min-width: 0;
  overflow: visible;
  flex-shrink: 1;
  flex: 0 1 auto;
  
  // 确保内部的tag也能正确收缩
  :deep(.t-tag) {
    max-width: 100% !important;
    min-width: 0 !important;
    width: auto !important;
    display: inline-flex !important;
  }
  
  :deep(.t-tag span),
  :deep(.t-tag > span) {
    display: block !important;
    overflow: hidden !important;
    text-overflow: ellipsis !important;
    white-space: nowrap !important;
    max-width: 100% !important;
    min-width: 0 !important;
  }
}

.faq-tag-tooltip {
  position: fixed;
  z-index: 9999;
  max-width: 320px;
  min-width: 100px;
  padding: 10px 14px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  box-shadow: 0 0 8px 0 rgba(0, 0, 0, 0.08);
  font-family: var(--app-font-family);
  font-size: 12px;
  font-weight: 400;
  line-height: 1.6;
  word-break: break-word;
  pointer-events: none;

  &::before {
    content: '';
    position: absolute;
    width: 0;
    height: 0;
    border: 5px solid transparent;
  }

  &.placement-top::before {
    bottom: -10px;
    left: 50%;
    transform: translateX(-50%);
    border-top-color: var(--td-component-stroke);
  }

  &.placement-top::after {
    content: '';
    position: absolute;
    bottom: -9px;
    left: 50%;
    transform: translateX(-50%);
    width: 0;
    height: 0;
    border: 5px solid transparent;
    border-top-color: var(--td-bg-color-container);
  }

  &.placement-bottom::before {
    top: -10px;
    left: 50%;
    transform: translateX(-50%);
    border-bottom-color: var(--td-component-stroke);
  }

  &.placement-bottom::after {
    content: '';
    position: absolute;
    top: -9px;
    left: 50%;
    transform: translateX(-50%);
    width: 0;
    height: 0;
    border: 5px solid transparent;
    border-bottom-color: var(--td-bg-color-container);
  }

  &.placement-left::before {
    right: -10px;
    top: 50%;
    transform: translateY(-50%);
    border-left-color: var(--td-component-stroke);
  }

  &.placement-left::after {
    content: '';
    position: absolute;
    right: -9px;
    top: 50%;
    transform: translateY(-50%);
    width: 0;
    height: 0;
    border: 5px solid transparent;
    border-left-color: var(--td-bg-color-container);
  }

  &.placement-right::before {
    left: -10px;
    top: 50%;
    transform: translateY(-50%);
    border-right-color: var(--td-component-stroke);
  }

  &.placement-right::after {
    content: '';
    position: absolute;
    left: -9px;
    top: 50%;
    transform: translateY(-50%);
    width: 0;
    height: 0;
    border: 5px solid transparent;
    border-right-color: var(--td-bg-color-container);
  }

  // 所有类型使用统一的常规边框颜色
  &.tooltip-answer,
  &.tooltip-similar,
  &.tooltip-negative {
    // 边框和箭头颜色已在主样式中定义为 #e7ebf0
    // 无需额外覆盖
  }
}

.tooltip-content {
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 12px;
  font-weight: 400;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s ease;
}

.fade-enter-from {
  opacity: 0;
}

.fade-leave-to {
  opacity: 0;
}
</style>

