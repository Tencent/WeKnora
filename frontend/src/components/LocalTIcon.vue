<template>
  <component :is="iconComponent" v-bind="$attrs" v-if="iconComponent" />
</template>

<script setup lang="ts">
import { computed } from 'vue';
import * as icons from 'tdesign-icons-vue-next';

const props = defineProps<{
  name?: string;
}>();

const iconComponent = computed(() => {
  if (!props.name) return null;
  
  // Convert kebab-case to PascalCase
  const camelName = props.name.replace(/-(\w)/g, (_, c) => c.toUpperCase());
  const pascalName = camelName.charAt(0).toUpperCase() + camelName.slice(1) + 'Icon';
  
  return (icons as any)[pascalName];
});
</script>
