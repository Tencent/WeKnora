<template>
  <div class="oauth-widget">
    <div class="oauth-widget__row">
      <span class="oauth-widget__status" :class="{ 'is-connected': connected }">
        <t-icon :name="connected ? 'check-circle-filled' : 'link'" />
        {{ connected ? t('schemaForm.oauth.connected') : t('schemaForm.oauth.notConnected') }}
      </span>
      <t-button
        size="small"
        :variant="connected ? 'outline' : 'base'"
        theme="primary"
        :loading="busy"
        :disabled="disabled || !source"
        @click="connect"
      >
        {{ connected ? t('schemaForm.oauth.reconnect') : t('schemaForm.oauth.connect') }}
      </t-button>
      <t-button v-if="connected && !required" size="small" variant="text" :disabled="disabled || busy" @click="clear">
        {{ t('schemaForm.oauth.disconnect') }}
      </t-button>
    </div>
    <p v-if="waitingExternal" class="oauth-widget__hint">
      {{ t('schemaForm.oauth.waitingBrowser') }}
      <t-link theme="primary" size="small" @click="cancel">{{ t('schemaForm.oauth.cancel') }}</t-link>
    </p>
    <p v-if="error" class="oauth-widget__error">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { isOAuthRef } from '../schema'
import { isOAuthResult, systemBrowser, useSchemaFormSource, type OAuthStart } from '../source'
import type { WidgetProps } from './types'

// Connects an account through the plugin's OAuth app (x-oauth). The popup
// runs the provider's consent screen; WeKnora keeps the tokens and the field
// only holds "oauth:<connection>". The callback page posts the result back
// to this window, and the widget also polls for it: the desktop app sends
// the consent screen to the system browser, and some providers cut a popup
// off from the window that opened it.
const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
const { t } = useI18n()

const POLL_EVERY = 2000
// A little past the server's ten minutes for an authorization.
const POLL_FOR = 11 * 60 * 1000
// After the popup closes, the callback may still be finishing.
const POLL_AFTER_CLOSE = 6000

const source = useSchemaFormSource()
const connected = computed(() => isOAuthRef(props.modelValue))
const busy = ref(false)
const error = ref('')
const waitingExternal = ref(false)
let cleanup: (() => void) | undefined

async function connect() {
  if (!source) return
  cleanup?.()
  error.value = ''
  const external = systemBrowser()
  // Open the window inside the click so popup blockers let it through.
  const popup = external ? null : window.open('', 'weknora-plugin-oauth', 'popup,width=560,height=720')
  if (!external && !popup) {
    error.value = t('schemaForm.oauth.popupBlocked')
    return
  }
  busy.value = true
  let start: OAuthStart
  try {
    start = await source.startOAuth(props.path ?? '')
  } catch (e) {
    popup?.close()
    busy.value = false
    error.value = (e as { message?: string })?.message || t('schemaForm.oauth.failed')
    return
  }
  if (external) {
    external(start.authorizeUrl)
    waitingExternal.value = true
  } else if (popup) {
    popup.location.href = start.authorizeUrl
  }

  const finish = (ok: boolean, connection: string | undefined, message: string | undefined) => {
    done()
    if (ok && isOAuthRef(connection)) emit('update:modelValue', connection)
    else error.value = message || t('schemaForm.oauth.failed')
  }
  const onMessage = (ev: MessageEvent) => {
    if (!isOAuthResult(ev, popup, start)) return
    finish(ev.data.ok, ev.data.connection, ev.data.error)
  }
  const startedAt = Date.now()
  let closedAt = 0
  let polling = false
  const tick = setInterval(async () => {
    if (popup?.closed && !closedAt) closedAt = Date.now()
    const now = Date.now()
    if (now - startedAt > POLL_FOR || (closedAt && now - closedAt > POLL_AFTER_CLOSE)) {
      done()
      return
    }
    if (!source.oauthResult || polling) return
    polling = true
    try {
      const res = await source.oauthResult(start.state)
      if (res.status === 'done' && cleanup) finish(!!res.ok, res.connection, res.error)
    } catch {
      // Try again on the next tick.
    } finally {
      polling = false
    }
  }, POLL_EVERY)
  const done = () => {
    window.removeEventListener('message', onMessage)
    clearInterval(tick)
    busy.value = false
    waitingExternal.value = false
    cleanup = undefined
  }
  cleanup = () => {
    done()
    if (popup && !popup.closed) popup.close()
  }
  window.addEventListener('message', onMessage)
}

function cancel() {
  cleanup?.()
}

function clear() {
  error.value = ''
  emit('update:modelValue', undefined)
}

onBeforeUnmount(() => cleanup?.())
</script>

<style lang="less" scoped>
.oauth-widget__row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.oauth-widget__status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-right: auto;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);

  &.is-connected {
    color: var(--td-success-color);
  }
}

.oauth-widget__hint {
  margin: 4px 0 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

.oauth-widget__error {
  margin: 4px 0 0;
  color: var(--td-error-color);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}
</style>
