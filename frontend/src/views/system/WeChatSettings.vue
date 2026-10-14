<template>
  <section class="wechat-settings" :aria-label="t('wechatSettings.title')">
    <t-loading v-if="loading" />
    <t-alert v-if="error" theme="error" :message="error" />
    <template v-if="settings && form">
      <t-alert theme="info" :message="t(`wechatSettings.source.${settings.source}`)" />
      <t-alert v-if="!settings.encryption_ready" theme="warning" :message="t('wechatSettings.encryptionHint')" />
      <p class="wechat-settings__hint">{{ t('wechatSettings.priorityHint') }}</p>
      <div class="wechat-settings__row">
        <div><label for="wechat-platform">{{ t('wechatSettings.platformId') }}</label><p>{{ t('wechatSettings.platformHint') }}</p></div>
        <t-input id="wechat-platform" v-model="form.platform_id" :disabled="saving" :maxlength="128" />
      </div>
      <div v-for="client in clients" :key="client" class="wechat-settings__app">
        <div class="wechat-settings__row">
          <h3>{{ t(`wechatSettings.${client}`) }}</h3>
          <t-switch v-model="form[client].enabled" :disabled="saving" :aria-label="t(`wechatSettings.${client}`)" />
        </div>
        <div class="wechat-settings__row">
          <label :for="`wechat-${client}-appid`">AppID</label>
          <t-input :id="`wechat-${client}-appid`" v-model="form[client].app_id" :disabled="saving" :maxlength="128" />
        </div>
        <div class="wechat-settings__row">
          <div><label :for="`wechat-${client}-secret`">AppSecret</label><p>{{ t(form[client].secret_configured ? 'wechatSettings.secretStored' : 'wechatSettings.secretMissing') }}</p></div>
          <div class="wechat-settings__credential">
            <t-input :id="`wechat-${client}-secret`" v-model="form[client].secret" type="password" autocomplete="new-password"
              :disabled="saving || form[client].clearSecret" :maxlength="512" :placeholder="t('wechatSettings.secretPlaceholder')" />
            <t-checkbox v-model="form[client].clearSecret" :disabled="saving">{{ t('wechatSettings.clearSecret') }}</t-checkbox>
          </div>
        </div>
        <div v-if="client === 'web'" class="wechat-settings__row">
          <div><label for="wechat-callback">{{ t('wechatSettings.callback') }}</label><p>{{ t('wechatSettings.callbackHint') }}</p></div>
          <t-input id="wechat-callback" v-model="form.web.callback_url" :disabled="saving" :maxlength="2048"
            placeholder="https://weknora.example.com/api/v1/auth/wechat/callback" />
        </div>
      </div>
      <div class="wechat-settings__actions">
        <t-button :loading="saving" @click="save">{{ t('common.save') }}</t-button>
        <t-button variant="outline" :disabled="saving" @click="reload">{{ t('wechatSettings.reload') }}</t-button>
      </div>
    </template>
    <t-button v-else-if="!loading" variant="outline" @click="reload">{{ t('wechatSettings.reload') }}</t-button>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { getWeChatSettings, updateWeChatSettings, type WeChatSettings } from '@/api/system/wechat'
import { weChatAppDraft, weChatAppUpdate, type WeChatAppDraft } from './wechatSettingsState'

const props = defineProps<{ active: boolean }>()
const { t } = useI18n()
const clients = ['web', 'mobile'] as const
const settings = ref<WeChatSettings>()
const form = ref<{ platform_id: string; web: WeChatAppDraft; mobile: WeChatAppDraft }>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
function apply(value: WeChatSettings) {
  settings.value = value
  form.value = { platform_id: value.platform_id, web: weChatAppDraft(value.web), mobile: weChatAppDraft(value.mobile) }
}
function failure(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } } }
  return e.response?.data?.error || t('wechatSettings.failed')
}
async function reload() {
  if (loading.value || saving.value) return
  loading.value = true
  error.value = ''
  try { apply(await getWeChatSettings()) } catch (err) { error.value = failure(err) }
  finally { loading.value = false }
}
async function save() {
  if (!form.value || !settings.value || saving.value || loading.value) return
  saving.value = true
  error.value = ''
  try {
    apply(await updateWeChatSettings({ version: settings.value.version, platform_id: form.value.platform_id.trim(),
      web: weChatAppUpdate(form.value.web), mobile: weChatAppUpdate(form.value.mobile) }))
    MessagePlugin.success(t('wechatSettings.saved'))
  } catch (err) { error.value = failure(err) }
  finally { saving.value = false }
}
watch(() => props.active, active => { if (active && !settings.value) reload() }, { immediate: true })
</script>

<style scoped lang="less">
.wechat-settings { display: flex; flex-direction: column; gap: 16px; }
.wechat-settings__hint, .wechat-settings__row p { color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.6; margin: 4px 0 0; }
.wechat-settings__row { display: grid; grid-template-columns: minmax(180px, 1fr) minmax(240px, 1.2fr); align-items: center; gap: 24px; padding: 12px 0; }
.wechat-settings__row h3 { margin: 0; font-size: 14px; font-weight: 600; }
.wechat-settings__row :deep(.t-switch) { justify-self: end; }
.wechat-settings__app { border-top: 1px solid var(--td-component-stroke); padding-top: 8px; }
.wechat-settings__credential { display: flex; flex-direction: column; gap: 8px; }
.wechat-settings__actions { display: flex; gap: 12px; justify-content: flex-end; }
@media (max-width: 640px) { .wechat-settings__row { grid-template-columns: 1fr; gap: 8px; } }
</style>
