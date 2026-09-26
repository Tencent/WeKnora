<template>
  <img v-if="url" :src="url" alt="" class="plugin-page-icon" :style="{ width: size, height: size }" />
  <t-icon v-else name="app" :size="size" />
</template>

<script setup lang="ts">
// A plugin page's icon on its tab: the page's own, else its plugin's, else
// the generic app glyph.
withDefaults(defineProps<{ url?: string; size?: string }>(), { url: undefined, size: '18px' })
</script>

<style scoped>
.plugin-page-icon {
  flex: none;
  object-fit: contain;
}
</style>
