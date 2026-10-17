<template>
  <span class="kb-type-avatar" :class="isFaq ? 'is-faq' : 'is-document'" aria-hidden="true">
    <svg v-if="isFaq" class="kb-type-icon kb-faq-icon" width="16" height="16" viewBox="0 0 24 24"
      fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
      <path d="M5 4h14a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H9l-5 3v-3a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z" />
      <g class="kb-faq-question">
        <path d="M9.5 9a2.5 2.5 0 0 1 5 .5c0 1.5-2.5 1.5-2.5 3" />
        <circle cx="12" cy="15.5" r="0.8" fill="currentColor" stroke="none" />
      </g>
    </svg>
    <svg v-else class="kb-type-icon kb-folder-icon" width="16" height="16" viewBox="0 0 24 24"
      fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
      <path d="M3 19V6a2 2 0 0 1 2-2h4l2 3h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2Z" />
      <g class="kb-folder-paper">
        <rect x="7" y="8" width="10" height="10" rx="1" />
        <path d="M10 11h4m-4 3h4" />
      </g>
      <path class="kb-folder-front" d="M3 9h18v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
    </svg>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ type?: string }>()
const isFaq = computed(() => props.type === 'faq')
</script>

<style scoped lang="less">
.kb-type-icon {
  display: block;
  flex-shrink: 0;
  overflow: visible;
  transform: var(--kb-icon-transform, none);
  transition: transform var(--app-motion-slow) cubic-bezier(0.2, 0.8, 0.2, 1);
}

.kb-folder-paper {
  fill: var(--td-bg-color-container);
  opacity: var(--kb-folder-paper-opacity, 0);
  transform: translateY(var(--kb-folder-paper-offset, 3px));
  transition: transform var(--app-motion-slow) cubic-bezier(0.2, 0.8, 0.2, 1), opacity var(--app-motion-fast) ease;
}

.kb-faq-question {
  transform-box: fill-box;
  transform-origin: center;
  transform: var(--kb-faq-question-transform, none);
  transition: transform var(--app-motion-slow) cubic-bezier(0.34, 1.56, 0.64, 1);
}

.kb-folder-front {
  fill: var(--td-bg-color-container);
  transform-box: fill-box;
  transform-origin: center bottom;
  transform: perspective(48px) rotateX(var(--kb-folder-open-angle, 0deg));
  transition: transform var(--app-motion-base) cubic-bezier(0.2, 0.8, 0.2, 1);
}

@media (prefers-reduced-motion: reduce) {
  .kb-type-icon, .kb-folder-front, .kb-folder-paper, .kb-faq-question {
    transform: none;
    transition: none;
  }
  .kb-folder-paper { opacity: 0; }
}

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
