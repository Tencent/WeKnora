<template>
  <section class="browser-tool-details">
    <p class="browser-result-status" :class="{ failed: browserToolIncomplete(event) }">{{ browserToolSummary(t, event) }}</p>
    <p v-if="content.error" class="browser-result-error">{{ content.error }}</p>
    <p v-if="content.recoveryHint" class="browser-result-hint">{{ content.recoveryHint }}</p>
    <p v-if="content.empty">{{ t('localBrowser.noEntries') }}</p>
    <p v-if="content.title" class="browser-page-title">{{ content.title }}</p>
    <p v-if="content.address" class="browser-page-address">{{ content.address }}</p>
    <p v-if="content.prompt" class="browser-help-prompt">{{ content.prompt }}</p>
    <img v-if="content.image" class="browser-result-image" :src="content.image" :alt="t('localBrowser.preview')" />
    <ul v-if="content.tabs.length" class="browser-result-tabs">
      <li v-for="(tab, index) in content.tabs" :key="index">
        <strong>{{ tab.title || t('localBrowser.untitledTab') }}</strong>
        <span v-if="tab.address">{{ tab.address }}</span>
      </li>
    </ul>
    <pre v-if="content.text" class="browser-page-content">{{ content.text }}</pre>
    <p v-if="content.truncated" class="browser-content-note">{{ t('localBrowser.contentTruncated') }}</p>
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { browserToolContent, browserToolSummary, browserToolIncomplete, type BrowserToolEvent } from '@/utils/browserToolDisplay'
const props = defineProps<{ event: BrowserToolEvent }>()
const { t } = useI18n()
const content = computed(() => browserToolContent(props.event))
</script>
<style scoped>
.browser-tool-details { min-width: 0; max-width: 100%; box-sizing: border-box; margin: 8px 0; padding: 12px 14px; border: 1px solid var(--td-component-border); border-radius: 8px; background: var(--td-bg-color-container); font-size: 13px; color: var(--td-text-color-secondary); overflow: hidden; }
.browser-tool-details p { margin: 0 0 8px; line-height: 1.6; overflow-wrap: anywhere; }
.browser-tool-details p:last-child { margin-bottom: 0; }
.browser-result-error, .browser-result-status.failed { color: var(--td-error-color); }
.browser-page-title { color: var(--td-text-color-primary); font-weight: 500; }
.browser-page-address, .browser-content-note { font-size: 12px; }
.browser-result-image { display: block; max-width: 100%; max-height: 360px; object-fit: contain; margin: 8px 0; border-radius: 6px; }
.browser-result-tabs { margin: 8px 0; padding: 0; list-style: none; max-height: 300px; overflow: auto; }
.browser-result-tabs li { display: grid; gap: 4px; padding: 8px 0; overflow-wrap: anywhere; }
.browser-result-tabs strong { color: var(--td-text-color-primary); font-weight: 500; }
.browser-result-tabs span { font-size: 12px; }
.browser-page-content { margin: 8px 0 0; max-height: 300px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; line-height: 1.6; padding: 10px; border-radius: 6px; background: var(--td-bg-color-secondarycontainer); }
</style>
