<template>
  <svg :width="width" :height="height" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"
    aria-hidden="true" focusable="false">
    <path d="M9 18H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v5M3 9h18" />
    <path d="M7 6.5h.01M10 6.5h.01M12 12l3 9 2-4 4-2-9-3Z" />
  </svg>
</template>
<script setup lang="ts">
withDefaults(defineProps<{
  width?: number | string
  height?: number | string
}>(), {
  width: 18,
  height: 18,
})
</script>
