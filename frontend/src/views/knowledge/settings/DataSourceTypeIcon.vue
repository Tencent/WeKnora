<script setup lang="ts">
import feishuIcon from '@/assets/img/datasource-feishu.ico'
import notionIcon from '@/assets/img/datasource-notion.ico'
import yuqueIcon from '@/assets/img/datasource-yuque.ico'

const props = withDefaults(defineProps<{
  type: string
  size?: number
}>(), {
  size: 20,
})

const iconMap: Record<string, string> = {
  feishu: feishuIcon,
  notion: notionIcon,
  yuque: yuqueIcon,
}

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
    :style="{ width: `${size}px`, height: `${size}px` }"
  >
    <img
      v-if="iconMap[type]"
      :src="iconMap[type]"
      :alt="type"
      :style="{ width: `${size}px`, height: `${size}px` }"
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
  border-radius: 6px;
  background: var(--td-bg-color-component);
}

.ds-type-icon img {
  display: block;
  object-fit: cover;
}

.ds-type-icon-fallback {
  font-size: 11px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}
</style>
