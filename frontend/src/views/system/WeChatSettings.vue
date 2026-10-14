<template>
  <section class="wechat-settings" :aria-label="t('wechatSettings.title')" :aria-busy="busy">
    <t-alert v-if="error" theme="error" :message="error" class="wechat-settings__alert" />
    <div v-if="loading && !settings" class="wechat-settings__loading">
      <t-loading :text="t('system.globalSettings.loading')" />
    </div>
    <div v-else-if="!settings" class="wechat-settings__loading">
      <t-button variant="outline" @click="reload">{{ t('wechatSettings.reload') }}</t-button>
    </div>

    <template v-if="settings && form">
      <div class="wechat-settings__source">
        <t-tag variant="light" size="small">{{ t(`wechatSettings.source.${settings.source}`) }}</t-tag>
        <span>{{ t('wechatSettings.priorityHint') }}</span>
      </div>
      <t-alert v-if="!settings.encryption_ready" theme="warning" :message="t('wechatSettings.encryptionHint')"
        class="wechat-settings__alert" />

      <div class="wechat-settings__group">
        <div class="wechat-settings__row">
          <div class="wechat-settings__info">
            <label class="setting-label" for="wechat-platform">{{ t('wechatSettings.platformId') }}</label>
            <p class="desc">{{ t('wechatSettings.platformHint') }}</p>
          </div>
          <div class="wechat-settings__control">
            <t-input id="wechat-platform" v-model="form.platform_id" :disabled="busy" :maxlength="128" />
          </div>
        </div>

        <div v-for="client in clients" :key="client" class="wechat-settings__app">
          <div class="wechat-settings__row wechat-settings__row--toggle">
            <div class="wechat-settings__info">
              <span :id="`wechat-${client}-title`" class="setting-label">{{ t(`wechatSettings.${client}`) }}</span>
              <p class="desc">{{ t(`wechatSettings.${client}Desc`) }}</p>
            </div>
            <div class="wechat-settings__control wechat-settings__control--switch">
              <t-switch v-model="form[client].enabled" :disabled="busy" :aria-labelledby="`wechat-${client}-title`" />
            </div>
          </div>

          <div v-if="form[client].enabled" class="wechat-settings__fields" role="group"
            :aria-labelledby="`wechat-${client}-title`">
            <div class="wechat-settings__field">
              <label class="wechat-settings__field-label" :for="`wechat-${client}-appid`">AppID</label>
              <div class="wechat-settings__input">
                <t-input :id="`wechat-${client}-appid`" v-model="form[client].app_id" :disabled="busy" :maxlength="128"
                  :status="fieldError(`${client}.app_id`) ? 'error' : undefined" />
                <p v-if="fieldError(`${client}.app_id`)" class="wechat-settings__error">{{ fieldError(`${client}.app_id`) }}</p>
              </div>
            </div>
            <div class="wechat-settings__field">
              <div>
                <label class="wechat-settings__field-label" :for="`wechat-${client}-secret`">AppSecret</label>
                <p class="wechat-settings__field-desc">
                  {{ t(form[client].secret_configured ? 'wechatSettings.secretStored' : 'wechatSettings.secretMissing') }}
                </p>
              </div>
              <div class="wechat-settings__input">
                <t-input :id="`wechat-${client}-secret`" :model-value="form[client].clearSecret ? '' : form[client].secret"
                  type="password" autocomplete="new-password" :disabled="busy || form[client].clearSecret" :maxlength="512"
                  :placeholder="t(form[client].clearSecret ? 'wechatSettings.secretCleared'
                    : form[client].secret_configured ? 'wechatSettings.secretPlaceholder' : 'wechatSettings.secretMissing')"
                  :status="fieldError(`${client}.secret`) ? 'error' : undefined"
                  @update:model-value="(value: string | number) => updateSecret(client, value)" />
                <p v-if="fieldError(`${client}.secret`)" class="wechat-settings__error">{{ fieldError(`${client}.secret`) }}</p>
                <div v-if="form[client].secret_configured" class="wechat-settings__secret-actions">
                  <t-button v-if="form[client].clearSecret" variant="text" size="small" :disabled="busy"
                    @click="form[client].clearSecret = false">{{ t('common.cancel') }}</t-button>
                  <t-popconfirm v-else :visible="active && clearConfirm === client"
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
            <div v-if="client === 'web'" class="wechat-settings__field">
              <div>
                <label class="wechat-settings__field-label" for="wechat-callback">{{ t('wechatSettings.callback') }}</label>
                <p class="wechat-settings__field-desc">{{ t('wechatSettings.callbackHint') }}</p>
              </div>
              <div class="wechat-settings__input">
                <t-input id="wechat-callback" v-model="form.web.callback_url" :disabled="busy" :maxlength="2048"
                  placeholder="https://weknora.example.com/api/v1/auth/wechat/callback"
                  :status="fieldError('web.callback_url') ? 'error' : undefined" />
                <p v-if="fieldError('web.callback_url')" class="wechat-settings__error">{{ fieldError('web.callback_url') }}</p>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="dirty || saving" class="wechat-settings__footer">
        <span class="wechat-settings__unsaved">{{ t('wechatSettings.unsaved') }}</span>
        <t-button variant="outline" :disabled="busy" @click="reload">{{ t('wechatSettings.discard') }}</t-button>
        <t-button theme="primary" :loading="saving" :disabled="loading" @click="save">{{ t('common.save') }}</t-button>
      </div>
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
  const [field, key] = Object.entries(errors.value)[0] || []
  if (field && key) {
    const message = t(`wechatSettings.${key}`)
    // Fields of a disabled app are collapsed, so their errors need a visible summary.
    const client = field.split('.')[0] as 'web' | 'mobile'
    error.value = form.value[client]?.enabled ? '' : message
    emit('announced', message)
    return
  }
  saving.value = true
  error.value = ''
  clearConfirm.value = null
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
  if (!active) clearConfirm.value = null
}, { immediate: true })
</script>

<style scoped lang="less">
@import (reference) '@/components/css/settings-section.less';

.wechat-settings {
  width: 100%;
  min-width: 0;

  &__alert { margin-bottom: 20px; }
  &__loading { display: flex; justify-content: center; padding: 60px 0; }

  &__source {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 20px;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
    line-height: 1.5;
  }

  &__group { display: flex; flex-direction: column; }

  &__row {
    .setting-row();
    display: grid;
    grid-template-columns: minmax(0, 1fr) 320px;
    gap: 32px;
    padding: 24px 0;
  }
  &__group > &__row:first-child { padding-top: 0; }

  &__info {
    .setting-info();
    max-width: none;
    .setting-label { display: block; }
    .desc { line-height: 1.65; max-width: 600px; }
  }
  &__control {
    .setting-control();
    min-width: 0;
    flex-direction: column;
    align-items: stretch;
    gap: 6px;
    &--switch { align-items: flex-end; }
  }

  &__app {
    border-bottom: 1px solid var(--td-component-stroke);
    &:last-child { border-bottom: none; }
    .wechat-settings__row--toggle { border-bottom: none; }
  }

  &__fields {
    display: flex;
    flex-direction: column;
    gap: 20px;
    margin-bottom: 24px;
    padding: 20px;
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-secondarycontainer);
  }
  &__field {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    align-items: start;
    gap: 32px;
  }
  &__field-label {
    display: block;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    font-weight: 500;
    line-height: 32px;
  }
  &__field-desc {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
    line-height: 1.5;
  }
  &__input { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  &__error {
    margin: 0;
    color: var(--td-error-color);
    font-size: var(--app-text-sm);
    line-height: 1.5;
  }
  &__secret-actions { display: flex; justify-content: flex-end; }

  &__footer {
    position: sticky;
    bottom: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 8px;
    padding: 12px 0;
    border-top: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
  }
  &__unsaved {
    margin-right: auto;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
  }
}

@container (max-width: 700px) {
  .wechat-settings__row,
  .wechat-settings__field { grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .wechat-settings__control--switch { align-items: flex-start; }
}
</style>
