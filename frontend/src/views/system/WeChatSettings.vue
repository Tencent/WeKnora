<template>
  <section class="wechat-settings" :aria-label="t('wechatSettings.title')" :aria-busy="busy">
    <Teleport to="#system-settings-actions">
      <div v-show="active" class="wechat-settings__actions">
        <span v-if="dirty" class="wechat-settings__unsaved">{{ t('wechatSettings.unsaved') }}</span>
        <t-button variant="text" size="small" class="wechat-settings__save" :loading="saving"
          :disabled="loading || !dirty" @click="save">
          <template #icon><t-icon name="save" size="16px" /></template>
          {{ t('common.save') }}
        </t-button>
      </div>
    </Teleport>
    <Teleport to="#system-wechat-toolbar">
      <div v-show="active" class="wechat-settings__tools">
        <t-popconfirm :visible="active && reloadConfirm" :content="t('wechatSettings.discardConfirm')"
          :popup-props="{ trigger: 'context-menu', overlayClassName: 'wk-popover', zIndex: 3050 }"
          placement="left" theme="warning" :confirm-btn="t('wechatSettings.reload')"
          :cancel-btn="t('common.cancel')" @confirm="reload" @cancel="reloadConfirm = false"
          @visible-change="(visible: boolean) => { if (!visible) reloadConfirm = false }">
          <t-button variant="text" shape="square" :loading="loading" :disabled="busy"
            :title="t('wechatSettings.reload')" :aria-label="t('wechatSettings.reload')" @click="requestReload">
            <template #icon><t-icon name="refresh" size="16px" /></template>
          </t-button>
        </t-popconfirm>
      </div>
    </Teleport>

    <t-alert v-if="error" theme="error" :message="error" class="wechat-settings__alert" />
    <div v-if="loading && !settings" class="wechat-settings__loading">
      <t-loading :text="t('system.globalSettings.loading')" />
    </div>
    <template v-if="settings && form">
      <div class="wechat-settings__summary">
        <t-tag variant="light" size="small">{{ t(`wechatSettings.source.${settings.source}`) }}</t-tag>
        <p>{{ t('wechatSettings.priorityHint') }}</p>
      </div>
      <t-alert v-if="!settings.encryption_ready" theme="warning" :message="t('wechatSettings.encryptionHint')"
        class="wechat-settings__alert" />
      <div class="wechat-settings__row">
        <div class="wechat-settings__info">
          <label for="wechat-platform">{{ t('wechatSettings.platformId') }}</label>
          <p class="desc">{{ t('wechatSettings.platformHint') }}</p>
        </div>
        <div class="wechat-settings__control">
          <t-input id="wechat-platform" v-model="form.platform_id" :disabled="busy" :maxlength="128"
            :aria-label="t('wechatSettings.platformId')" />
        </div>
      </div>
      <section v-for="client in clients" :key="client" class="wechat-settings__app"
        :aria-labelledby="`wechat-${client}-title`">
        <div class="wechat-settings__app-header">
          <h3 :id="`wechat-${client}-title`">{{ t(`wechatSettings.${client}`) }}</h3>
          <t-switch v-model="form[client].enabled" :disabled="busy" :aria-label="t(`wechatSettings.${client}`)" />
        </div>
        <div class="wechat-settings__row">
          <div class="wechat-settings__info"><label :for="`wechat-${client}-appid`">AppID</label></div>
          <div class="wechat-settings__control">
            <t-input :id="`wechat-${client}-appid`" v-model="form[client].app_id" :disabled="busy" :maxlength="128"
              :aria-label="`${t(`wechatSettings.${client}`)} AppID`"
              :status="fieldError(`${client}.app_id`) ? 'error' : undefined" :tips="fieldError(`${client}.app_id`)" />
          </div>
        </div>
        <div class="wechat-settings__row">
          <div class="wechat-settings__info">
            <label :for="`wechat-${client}-secret`">AppSecret</label>
            <p class="desc">{{ t(form[client].secret_configured ? 'wechatSettings.secretStored' : 'wechatSettings.secretMissing') }}</p>
          </div>
          <div class="wechat-settings__control">
            <t-input :id="`wechat-${client}-secret`" :model-value="form[client].clearSecret ? '' : form[client].secret" type="password" autocomplete="new-password"
              @update:model-value="(value: string | number) => updateSecret(client, value)"
              :disabled="busy || form[client].clearSecret" :maxlength="512"
              :aria-label="`${t(`wechatSettings.${client}`)} AppSecret`"
              :placeholder="t(form[client].clearSecret ? 'wechatSettings.secretCleared' : form[client].secret_configured
                ? 'wechatSettings.secretPlaceholder' : 'wechatSettings.secretMissing')"
              :status="fieldError(`${client}.secret`) ? 'error' : undefined" :tips="fieldError(`${client}.secret`)" />
            <div v-if="form[client].secret_configured" class="wechat-settings__credential-actions">
              <t-button v-if="form[client].clearSecret" variant="text" size="small" :disabled="busy"
                @click="form[client].clearSecret = false">{{ t('common.cancel') }}</t-button>
              <t-popconfirm v-else-if="form[client].secret_configured" :visible="active && clearConfirm === client"
                :content="t('wechatSettings.clearConfirm')" placement="left" theme="warning"
                :popup-props="{ overlayClassName: 'wk-popover', zIndex: 3050 }"
                :confirm-btn="{ content: t('wechatSettings.clearSecret'), theme: 'danger' }"
                :cancel-btn="t('common.cancel')" @confirm="clearSecret(client)"
                @visible-change="(visible: boolean) => { clearConfirm = visible ? client : null }">
                <t-button variant="text" size="small" :disabled="busy">{{ t('wechatSettings.clearSecret') }}</t-button>
              </t-popconfirm>
            </div>
          </div>
        </div>
        <div v-if="client === 'web'" class="wechat-settings__row">
          <div class="wechat-settings__info">
            <label for="wechat-callback">{{ t('wechatSettings.callback') }}</label>
            <p class="desc">{{ t('wechatSettings.callbackHint') }}</p>
          </div>
          <div class="wechat-settings__control">
            <t-input id="wechat-callback" v-model="form.web.callback_url" :disabled="busy" :maxlength="2048"
              :aria-label="t('wechatSettings.callback')" placeholder="https://weknora.example.com/api/v1/auth/wechat/callback"
              :status="fieldError('web.callback_url') ? 'error' : undefined" :tips="fieldError('web.callback_url')" />
          </div>
        </div>
      </section>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { getWeChatSettings, updateWeChatSettings, type WeChatSettings } from '@/api/system/wechat'
import {
  weChatAppDraft, weChatAppUpdate, weChatSettingsDirty, weChatSettingsErrors, type WeChatSettingsDraft,
} from './wechatSettingsState'

const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{ announced: [string] }>()
const { t } = useI18n()
const clients = ['web', 'mobile'] as const
const settings = ref<WeChatSettings>()
const form = ref<WeChatSettingsDraft>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const reloadConfirm = ref(false)
const clearConfirm = ref<'web' | 'mobile' | null>(null)
const validationAttempted = ref(false)
const busy = computed(() => loading.value || saving.value)
const dirty = computed(() => !!form.value && !!settings.value && weChatSettingsDirty(form.value, settings.value))
const errors = computed(() => form.value && settings.value ? weChatSettingsErrors(form.value, settings.value) : {})
function fieldError(field: string): string | undefined {
  return validationAttempted.value && errors.value[field] ? t(`wechatSettings.${errors.value[field]}`) : undefined
}
function apply(value: WeChatSettings) {
  settings.value = value
  form.value = { platform_id: value.platform_id, web: weChatAppDraft(value.web), mobile: weChatAppDraft(value.mobile) }
  validationAttempted.value = false
  clearConfirm.value = null
}
function failure(err: unknown): string {
  const e = err as { response?: { status?: number; data?: { error?: string } } }
  if (e.response?.status === 409) return t('wechatSettings.conflict')
  return e.response?.data?.error || t('wechatSettings.failed')
}
function requestReload() {
  if (busy.value) return
  if (dirty.value) reloadConfirm.value = true
  else void reload()
}
function updateSecret(client: 'web' | 'mobile', value: string | number) {
  if (form.value && !busy.value) form.value[client].secret = String(value)
}
function clearSecret(client: 'web' | 'mobile') {
  if (!form.value || busy.value) return
  form.value[client].clearSecret = true
  clearConfirm.value = null
}
async function reload() {
  if (busy.value) return
  reloadConfirm.value = false
  clearConfirm.value = null
  loading.value = true
  error.value = ''
  try {
    apply(await getWeChatSettings())
  } catch (err) {
    error.value = failure(err)
    emit('announced', error.value)
  } finally { loading.value = false }
}
async function save() {
  if (!form.value || !settings.value || busy.value || !dirty.value) return
  validationAttempted.value = true
  if (Object.keys(errors.value).length) {
    emit('announced', t(`wechatSettings.${Object.values(errors.value)[0]}`))
    return
  }
  saving.value = true
  error.value = ''
  clearConfirm.value = null
  reloadConfirm.value = false
  try {
    apply(await updateWeChatSettings({
      version: settings.value.version, platform_id: form.value.platform_id.trim(),
      web: weChatAppUpdate(form.value.web), mobile: weChatAppUpdate(form.value.mobile),
    }))
    const message = t('wechatSettings.saved')
    MessagePlugin.success(message)
    emit('announced', message)
  } catch (err) {
    error.value = failure(err)
    emit('announced', error.value)
  } finally { saving.value = false }
}
watch(() => props.active, active => {
  if (active && !dirty.value) void reload()
  if (!active) { reloadConfirm.value = false; clearConfirm.value = null }
}, { immediate: true })
</script>

<style scoped lang="less">
@import (reference) '@/components/css/settings-section.less';

.wechat-settings {
  width: 100%;
  min-width: 0;

  &__actions, &__tools { display: flex; align-items: center; gap: 8px; }
  &__save.t-button { .settings-list-action(); gap: 6px; }
  &__unsaved { color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
  &__alert { margin-bottom: 20px; }
  &__loading { display: flex; justify-content: center; padding: 60px 0; }
  &__summary {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px 12px;
    margin-bottom: 24px;
    p { margin: 0; color: var(--td-text-color-secondary); font-size: var(--app-text-md); line-height: 1.65; }
  }
  &__row {
    .setting-row();
    display: grid;
    grid-template-columns: minmax(0, 1fr) 320px;
    gap: 32px;
    padding: 24px 0;
  }
  > .wechat-settings__row { padding-top: 0; border-bottom: 0; }
  &__info { .setting-info(); max-width: none; }
  &__control {
    .setting-control();
    min-width: 0;
    flex-direction: column;
    align-items: stretch;
    gap: 6px;
    :deep(.t-input) { border-radius: var(--app-radius-sm); }
  }
  &__app { border-top: 1px solid var(--td-component-stroke); }
  &__app-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 24px 0 0;
    h3 { margin: 0; color: var(--td-text-color-primary); font-size: var(--app-text-lg); font-weight: 600; }
  }
  &__credential-actions { display: flex; justify-content: flex-end; }
}
@container (max-width: 700px) {
  .wechat-settings__row { grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .wechat-settings__actions { flex-wrap: wrap; justify-content: flex-end; }
}
</style>
