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
    <p v-if="error" class="oauth-widget__error">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { isOAuthRef } from '../schema'
import { isOAuthResult, useSchemaFormSource, type OAuthStart } from '../source'
import type { WidgetProps } from './types'

// Connects an account through the plugin's OAuth app (x-oauth). The popup
// runs the provider's consent screen; WeKnora keeps the tokens and the field
// only holds "oauth:<connection>".
const props = defineProps<WidgetProps>()
const emit = defineEmits<{ 'update:modelValue': [value: unknown] }>()
const { t } = useI18n()

const source = useSchemaFormSource()
const connected = computed(() => isOAuthRef(props.modelValue))
const busy = ref(false)
const error = ref('')
let cleanup: (() => void) | undefined

async function connect() {
  if (!source) return
  cleanup?.()
  error.value = ''
  // Open the window inside the click so popup blockers let it through.
  const popup = window.open('', 'weknora-plugin-oauth', 'popup,width=560,height=720')
  if (!popup) {
    error.value = t('schemaForm.oauth.popupBlocked')
    return
  }
  busy.value = true
  let start: OAuthStart
  try {
    start = await source.startOAuth(props.path ?? '')
  } catch (e) {
    popup.close()
    busy.value = false
    error.value = (e as { message?: string })?.message || t('schemaForm.oauth.failed')
    return
  }
  popup.location.href = start.authorizeUrl

  const onMessage = (ev: MessageEvent) => {
    if (!isOAuthResult(ev, popup, start)) return
    done()
    if (ev.data.ok && isOAuthRef(ev.data.connection)) emit('update:modelValue', ev.data.connection)
    else error.value = ev.data.error || t('schemaForm.oauth.failed')
  }
  const closed = setInterval(() => {
    if (popup.closed) done()
  }, 500)
  const done = () => {
    window.removeEventListener('message', onMessage)
    clearInterval(closed)
    busy.value = false
    cleanup = undefined
  }
  cleanup = () => {
    done()
    if (!popup.closed) popup.close()
  }
  window.addEventListener('message', onMessage)
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

.oauth-widget__error {
  margin: 4px 0 0;
  color: var(--td-error-color);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}
</style>
