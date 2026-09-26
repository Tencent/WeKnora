<template>
  <div class="plugin-frame" :class="{ 'plugin-frame--fill': fill }">
    <iframe
      ref="frame"
      :key="src"
      class="plugin-frame__iframe"
      :src="src"
      :title="title"
      sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"
      referrerpolicy="no-referrer"
      :style="fill ? undefined : { height: `${height}px` }"
    />
    <div v-if="!ready" class="plugin-frame__status">
      <t-loading v-if="!stalled" size="small" />
      <span v-else>{{ t('pluginPages.notResponding') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'

import { pluginPageRequest } from '@/api/plugin'
import { useAuthStore } from '@/stores/auth'
import { getApiBaseUrl } from '@/utils/api-base'
import { localizedText } from '@/utils/localizedText'

import { BridgeCallError, createBridgeHost, readTheme, type BridgeInit } from './bridgeHost'
import { pageFileUrl, type PluginPage } from './pluginPages'

// One plugin page in a sandboxed iframe: an opaque origin (no
// allow-same-origin), so it cannot read WeKnora's storage or call its API.
// It talks to the app only through the bridge, which answers messages from
// this iframe alone. Links it opens in a new window leave the sandbox: the
// new window has its own origin and no way back into the app.
const props = withDefaults(
  defineProps<{
    page: PluginPage
    /** What the mount tells the page, e.g. { knowledgeBaseId }. */
    context?: Record<string, unknown>
    /** Fill the container instead of growing with the page's content. */
    fill?: boolean
  }>(),
  { context: () => ({}), fill: false },
)
const emit = defineEmits<{ close: [] }>()

const { t, locale } = useI18n()
const auth = useAuthStore()
const router = useRouter()
const frame = ref<HTMLIFrameElement | null>(null)
const height = ref(240)
const ready = ref(false)
const stalled = ref(false)

const src = computed(() => pageFileUrl(getApiBaseUrl(), props.page))
const title = computed(() => localizedText(props.page.name, locale.value))

const initData = (): BridgeInit => ({
  pluginId: props.page.pluginId,
  version: props.page.version,
  mount: props.page.mount,
  locale: locale.value,
  role: auth.canAccessAllTenants ? 'admin' : auth.currentTenantRole || 'viewer',
  theme: readTheme(),
  context: { ...props.context },
})

const host = createBridgeHost({
  frame: () => frame.value?.contentWindow,
  init: () => {
    ready.value = true
    return initData()
  },
  handlers: {
    async apiRequest({ method, path, body }) {
      try {
        const res = await pluginPageRequest(props.page.pluginId, { mount: props.page.mount, method, path, body })
        return { status: res.data.status, body: res.data.body ?? null }
      } catch (e: any) {
        throw new BridgeCallError(e?.message || t('pluginPages.requestFailed'), e?.status)
      }
    },
    toast(message, theme) {
      MessagePlugin[theme](message)
    },
    confirm(message, heading) {
      return new Promise<boolean>((resolve) => {
        const dialog = DialogPlugin.confirm({
          header: heading || title.value,
          body: message,
          onConfirm: () => {
            dialog.destroy()
            resolve(true)
          },
          onClose: () => {
            dialog.destroy()
            resolve(false)
          },
        })
      })
    },
    navigate(path) {
      void router.push(path)
    },
    resize(h) {
      height.value = Math.max(h, 48)
    },
    close() {
      emit('close')
    },
  },
})

const onMessage = (event: MessageEvent) => host.handle(event)
let themeObserver: MutationObserver | null = null
let stallTimer: ReturnType<typeof setTimeout> | undefined

// A page that never says hello (a script error, a blocked module) would
// spin forever; say so after a while.
function watchStall() {
  ready.value = false
  stalled.value = false
  clearTimeout(stallTimer)
  stallTimer = setTimeout(() => {
    stalled.value = !ready.value
  }, 10_000)
}

onMounted(() => {
  window.addEventListener('message', onMessage)
  themeObserver = new MutationObserver(() => host.send('theme', readTheme()))
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['theme-mode'] })
  watchStall()
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  themeObserver?.disconnect()
  clearTimeout(stallTimer)
})

watch(src, watchStall)
watch(locale, (value) => host.send('locale', value))
// A mount whose context changes (another knowledge base) re-initialises the
// page with it.
watch(
  () => JSON.stringify(props.context),
  () => {
    if (ready.value) host.send('init', initData())
  },
)
</script>

<style lang="less" scoped>
.plugin-frame {
  position: relative;
  width: 100%;

  &--fill {
    flex: 1;
    min-height: 0;
    display: flex;
  }

  &__iframe {
    display: block;
    width: 100%;
    border: none;
    background: transparent;
  }

  &--fill &__iframe {
    flex: 1;
    height: 100%;
  }

  &__status {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    pointer-events: none;
  }
}
</style>
