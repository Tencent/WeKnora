<template>
  <span class="kb-type-avatar" :class="isFaq ? 'is-faq' : 'is-document'" aria-hidden="true">
    <t-icon :name="isFaq ? 'chat-bubble-help' : 'folder'" size="16px" />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ type?: string }>()
const isFaq = computed(() => props.type === 'faq')
</script>

<style scoped lang="less">
.kb-type-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-md);

  &.is-document {
    background: linear-gradient(135deg, color-mix(in srgb, var(--td-brand-color) 15%, transparent) 0%, color-mix(in srgb, var(--td-brand-color) 8%, transparent) 100%);
    color: var(--td-brand-color-active);
  }

  &.is-faq {
    background: linear-gradient(135deg, color-mix(in srgb, var(--weknora-faq-color, #0052d9) 15%, transparent) 0%, color-mix(in srgb, var(--weknora-faq-color, #0052d9) 8%, transparent) 100%);
    color: var(--weknora-faq-color, #0052d9);
  }
}
</style>
