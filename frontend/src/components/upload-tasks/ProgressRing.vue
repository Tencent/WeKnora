<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  /** 0..1 */
  value: number
  size?: number
  stroke?: number
}>(), {
  size: 20,
  stroke: 2.5,
})

const radius = computed(() => (props.size - props.stroke) / 2)
const circumference = computed(() => 2 * Math.PI * radius.value)
const offset = computed(() => circumference.value * (1 - Math.min(1, Math.max(0, props.value))))
</script>

<template>
  <svg
    class="progress-ring"
    :width="size"
    :height="size"
    :viewBox="`0 0 ${size} ${size}`"
    aria-hidden="true"
  >
    <circle
      class="progress-ring-track"
      :cx="size / 2"
      :cy="size / 2"
      :r="radius"
      :stroke-width="stroke"
    />
    <circle
      v-if="value > 0"
      class="progress-ring-value"
      :cx="size / 2"
      :cy="size / 2"
      :r="radius"
      :stroke-width="stroke"
      :stroke-dasharray="circumference"
      :stroke-dashoffset="offset"
    />
  </svg>
</template>

<style scoped lang="less">
.progress-ring {
  display: block;
  transform: rotate(-90deg);
}

.progress-ring-track,
.progress-ring-value {
  fill: none;
}

.progress-ring-track {
  stroke: color-mix(in srgb, currentColor 18%, transparent);
}

.progress-ring-value {
  stroke: currentColor;
  stroke-linecap: round;
  transition: stroke-dashoffset var(--app-motion-slow) ease;
}
</style>
