<template>
  <t-drawer
    :visible="visible"
    :header="title"
    size="480px"
    :footer="false"
    @close="emit('update:visible', false)"
  >
    <div class="preview-body">
      <t-tabs v-model="tab">
        <t-tab-panel value="iframe" :label="$t('embedPublish.tabIframe')">
          <p class="preview-hint">{{ $t('embedPublish.previewIframeHint') }}</p>
          <div class="iframe-shell">
            <iframe
              v-if="iframeSrc"
              :src="iframeSrc"
              class="preview-iframe"
              allow="clipboard-write"
            />
          </div>
        </t-tab-panel>
        <t-tab-panel value="widget" :label="$t('embedPublish.tabWidget')">
          <p class="preview-hint">{{ $t('embedPublish.previewWidgetHint') }}</p>
          <div class="widget-shell" :class="`pos-${position}`">
            <div class="widget-mock-page">{{ $t('embedPublish.previewMockPage') }}</div>
            <button
              type="button"
              class="widget-launcher"
              :style="{ background: primaryColor || '#0052d9' }"
              @click="widgetOpen = !widgetOpen"
            >
              {{ widgetOpen ? '✕' : '💬' }}
            </button>
            <div v-show="widgetOpen" class="widget-panel">
              <iframe
                v-if="iframeSrc"
                :src="iframeSrc"
                class="preview-iframe"
                allow="clipboard-write"
              />
            </div>
          </div>
        </t-tab-panel>
      </t-tabs>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { buildEmbedURL, type WidgetPosition } from '@/api/embed'

const props = defineProps<{
  visible: boolean
  channelId: string
  token: string
  title?: string
  primaryColor?: string
  position?: WidgetPosition
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
}>()

const tab = ref<'iframe' | 'widget'>('iframe')
const widgetOpen = ref(true)

const iframeSrc = computed(() => {
  if (!props.channelId || !props.token) return ''
  return buildEmbedURL(props.channelId, props.token)
})

watch(() => props.visible, (open) => {
  if (open) {
    tab.value = 'iframe'
    widgetOpen.value = true
  }
})
</script>

<style scoped>
.preview-body { display: flex; flex-direction: column; gap: 12px; }
.preview-hint {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}
.iframe-shell {
  border: 1px solid var(--td-component-border);
  border-radius: 12px;
  overflow: hidden;
  height: 520px;
  background: #f5f7fa;
}
.preview-iframe {
  width: 100%;
  height: 100%;
  border: none;
  background: #fff;
}
.widget-shell {
  position: relative;
  height: 520px;
  border: 1px solid var(--td-component-border);
  border-radius: 12px;
  overflow: hidden;
  background: #eef2f8;
}
.widget-mock-page {
  padding: 24px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}
.widget-launcher {
  position: absolute;
  width: 48px;
  height: 48px;
  border: none;
  border-radius: 50%;
  color: #fff;
  font-size: 20px;
  cursor: pointer;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.18);
  z-index: 2;
}
.widget-panel {
  position: absolute;
  width: 360px;
  max-width: calc(100% - 24px);
  height: 460px;
  max-height: calc(100% - 80px);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
  background: #fff;
  z-index: 1;
}
.pos-bottom-right .widget-launcher { right: 16px; bottom: 16px; }
.pos-bottom-right .widget-panel { right: 16px; bottom: 72px; }
.pos-bottom-left .widget-launcher { left: 16px; bottom: 16px; }
.pos-bottom-left .widget-panel { left: 16px; bottom: 72px; }
.pos-top-right .widget-launcher { right: 16px; top: 16px; }
.pos-top-right .widget-panel { right: 16px; top: 72px; }
.pos-top-left .widget-launcher { left: 16px; top: 16px; }
.pos-top-left .widget-panel { left: 16px; top: 72px; }
</style>
