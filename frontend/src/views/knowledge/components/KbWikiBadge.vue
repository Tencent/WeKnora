<template>
  <t-tooltip :content="t('knowledgeList.features.wiki')" placement="top">
    <span class="kb-wiki-badge" role="img" :aria-label="t('knowledgeList.features.wiki')">
      <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
        <path d="M12 7v14" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" />
        <path d="M16 12h2" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" />
        <path d="M16 8h2" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" />
        <path
          d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"
          stroke="currentColor"
          stroke-width="1.75"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
        <path d="M6 12h2" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" />
        <path d="M6 8h2" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </span>
  </t-tooltip>
</template>

<script setup lang="ts">
// Icon: Lucide "book-open-text" (ISC) — https://lucide.dev/icons/book-open-text
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<style scoped lang="less">
.kb-wiki-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: var(--td-text-color-secondary);

  svg {
    width: 15px;
    height: 15px;
    display: block;
  }
}
</style>
