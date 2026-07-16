<template>
  <svg
    v-if="iconDef"
    class="svg-icon"
    :class="customClass"
    :viewBox="iconDef.viewBox"
    :width="resolvedSize"
    :height="resolvedSize"
    :style="svgStyle"
    xmlns="http://www.w3.org/2000/svg"
    fill="none"
    aria-hidden="true"
  >
    <!-- eslint-disable-next-line vue/no-v-html -->
    <g v-html="iconDef.content" />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getIcon, hasIcon, themeColorMap, type IconName, type IconVariant } from './registry'

const props = withDefaults(
  defineProps<{
    /** 图标名称 */
    name: string
    /** 尺寸，单位 px */
    size?: number | string
    /** 自定义颜色，如 #07c05f 或 var(--td-brand-color) */
    color?: string
    /** 图标变体：default/green/active/thin/grey */
    variant?: IconVariant
    /** 主题预设：default | brand | secondary | placeholder | anti */
    theme?: 'default' | 'brand' | 'secondary' | 'placeholder' | 'anti'
    /** 自定义类名 */
    class?: string
  }>(),
  {
    size: 20,
    variant: 'default',
    theme: 'default',
  }
)

const customClass = computed(() => props.class)

const iconDef = computed(() => {
  if (!hasIcon(props.name)) return null
  return getIcon(props.name as IconName, props.variant)
})

const resolvedSize = computed(() => {
  const s = props.size
  if (typeof s === 'number') return `${s}px`
  return s
})

const svgStyle = computed(() => {
  const variantColorMap: Partial<Record<IconVariant, string>> = {
    green: themeColorMap.brand,
    grey: themeColorMap.placeholder,
  }
  const color =
    props.color ??
    variantColorMap[props.variant] ??
    themeColorMap[props.theme] ??
    themeColorMap.default
  return { color }
})
</script>

<style scoped>
.svg-icon {
  display: inline-block;
  vertical-align: middle;
  flex-shrink: 0;
}
/* 图标内容使用 currentColor，由根 style.color 控制 */
</style>
