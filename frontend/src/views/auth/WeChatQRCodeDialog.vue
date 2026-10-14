<template>
  <t-dialog :visible="visible" :header="t('auth.wechatQRTitle')" :footer="false" width="380px" placement="center"
    destroy-on-close class="wechat-qr-dialog" @close="emit('update:visible', false)">
    <div class="wechat-qr">
      <div class="wechat-qr__frame">
        <iframe v-if="src && !expired" :key="src" :src="src" :title="t('auth.wechatQRTitle')"
          class="wechat-qr__iframe" :class="{ 'is-ready': ready }" scrolling="no" frameborder="0" />
        <div v-if="!ready || expired || failed" class="wechat-qr__overlay" role="status">
          <template v-if="failed || expired">
            <t-icon :name="failed ? 'error-circle' : 'time'" size="32px" class="wechat-qr__state-icon" />
            <p>{{ t(failed ? 'auth.wechatQRLoadFailed' : 'auth.wechatQRExpired') }}</p>
            <t-button variant="outline" size="small" @click="load">{{ t('auth.wechatQRRefresh') }}</t-button>
          </template>
          <t-loading v-else size="small" />
        </div>
      </div>
      <p class="wechat-qr__hint">{{ t(inWeChat ? 'auth.wechatQRInWeChat' : 'auth.wechatQRHint') }}</p>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { createWeChatWebAttempt } from '@/api/auth'
import { isWeChatQRCodeReady, weChatQRCodeURL } from './wechatQRCode'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ 'update:visible': [boolean] }>()
const { t, locale } = useI18n()

// Some WeChat builds never post the ready message; stop the spinner anyway.
const READY_FALLBACK_MS = 6000

const src = ref('')
const ready = ref(false)
const expired = ref(false)
const failed = ref(false)
const inWeChat = typeof navigator !== 'undefined' && /MicroMessenger/i.test(navigator.userAgent)
let expiryTimer: ReturnType<typeof setTimeout> | undefined
let readyTimer: ReturnType<typeof setTimeout> | undefined
let generation = 0

function clearTimers() {
  clearTimeout(expiryTimer)
  clearTimeout(readyTimer)
}

function onMessage(event: MessageEvent) {
  if (isWeChatQRCodeReady(event)) ready.value = true
}

async function load() {
  const current = ++generation
  clearTimers()
  src.value = ''
  ready.value = false
  expired.value = false
  failed.value = false
  try {
    const response = await createWeChatWebAttempt()
    if (current !== generation) return
    if (!response?.success || !response.data) throw new Error('attempt failed')
    const colorScheme = document.documentElement.getAttribute('theme-mode') === 'dark' ? 'dark' : 'light'
    src.value = weChatQRCodeURL(response.data, { lang: locale.value, colorScheme })
    readyTimer = setTimeout(() => { ready.value = true }, READY_FALLBACK_MS)
    // The server rejects the attempt after expiry, so replace the code before WeChat shows its own refresh.
    const ttl = new Date(response.data.expires_at).getTime() - Date.now()
    expiryTimer = setTimeout(() => { expired.value = true }, Math.max(ttl - 5000, 0))
  } catch {
    if (current === generation) failed.value = true
  }
}

watch(() => props.visible, visible => {
  if (visible) {
    window.addEventListener('message', onMessage)
    void load()
  } else {
    generation++
    clearTimers()
    window.removeEventListener('message', onMessage)
    src.value = ''
  }
}, { immediate: true })

onBeforeUnmount(() => {
  clearTimers()
  window.removeEventListener('message', onMessage)
})
</script>

<style scoped lang="less">
.wechat-qr {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 4px 0 8px;

  &__frame {
    position: relative;
    width: 300px;
    height: 400px;
    overflow: hidden;
    border-radius: var(--app-radius-md);
  }

  &__iframe {
    width: 300px;
    height: 400px;
    border: 0;
    opacity: 0;
    transition: opacity var(--app-motion-base) ease;

    &.is-ready { opacity: 1; }
  }

  &__overlay {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    background: var(--td-bg-color-container);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
    text-align: center;

    p { margin: 0; }
  }

  &__state-icon { color: var(--td-text-color-placeholder); }

  &__hint {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
    line-height: 1.5;
    text-align: center;
  }
}
</style>
