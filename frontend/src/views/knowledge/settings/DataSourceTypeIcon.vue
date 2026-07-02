<script setup lang="ts">
import { getDatasourceIconUrl, datasourceIconMap } from './datasourceIcons'

const props = withDefaults(defineProps<{
  type: string
  size?: number
  /** inline: 类型选择等小尺寸场景；badge: 嵌入 ds-card__badge 等父级徽章容器 */
  variant?: 'inline' | 'badge'
}>(), {
  size: 20,
  variant: 'inline',
})

const iconMap = datasourceIconMap

function fallbackText(type: string) {
  switch (type) {
    case 'feishu':
      return 'F'
    case 'notion':
      return 'N'
    case 'yuque':
      return 'Y'
    default:
      return type.slice(0, 1).toUpperCase() || '?'
  }
}
</script>

<template>
  <span
    class="ds-type-icon"
    :class="`ds-type-icon--${variant}`"
    :style="variant === 'inline' ? { width: `${size}px`, height: `${size}px` } : undefined"
  >
    <img
      v-if="iconMap[type]"
      :src="iconMap[type]"
      :alt="type"
      class="ds-type-icon__img"
      :style="variant === 'inline' ? { width: `${size}px`, height: `${size}px` } : undefined"
    >
    <span v-else class="ds-type-icon-fallback">{{ fallbackText(type) }}</span>
  </span>
</template>

<style scoped>
.ds-type-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  overflow: hidden;
}

.ds-type-icon--inline {
  border-radius: 6px;
  background: var(--td-bg-color-component);
}

.ds-type-icon--inline .ds-type-icon__img {
  display: block;
  object-fit: contain;
}

.ds-type-icon--inline .ds-type-icon-fallback {
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}

.ds-type-icon--badge {
  width: 100%;
  height: 100%;
  background: transparent;
}

.ds-type-icon--badge .ds-type-icon__img {
  display: block;
  width: 24px;
  height: 24px;
  object-fit: contain;
}

.ds-type-icon--badge .ds-type-icon-fallback {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: inherit;
}
</style>
