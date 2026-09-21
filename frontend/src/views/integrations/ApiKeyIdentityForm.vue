<template>
  <section class="identity-form">
    <label class="identity-form__title">{{ $t('integrations.api.principalMode') }}</label>
    <p>{{ $t('integrations.api.keyIdentityDesc') }}</p>
    <t-radio-group :model-value="modelValue.mode" @change="patch({ mode: $event as APIPrincipalMode })">
      <t-radio-button value="tenant">{{ $t('integrations.api.modeTenant') }}</t-radio-button>
      <t-radio-button value="direct_header">{{ $t('integrations.api.modeDirect') }}</t-radio-button>
      <t-radio-button value="signed_token">{{ $t('integrations.api.modeSigned') }}</t-radio-button>
    </t-radio-group>
    <template v-if="modelValue.mode === 'direct_header'">
      <p>{{ $t('integrations.api.directWarning') }}</p>
      <code>X-External-User-ID</code>
      <div class="identity-form__switch">
        <label>{{ $t('integrations.api.requireDirectHeader') }}</label>
        <t-switch :model-value="modelValue.require_direct_header === true" @change="patch({ require_direct_header: Boolean($event) })" />
      </div>
      <p>{{ $t('integrations.api.requireDirectHeaderDesc') }}</p>
    </template>
    <template v-if="modelValue.mode === 'signed_token'">
      <p>{{ $t('integrations.api.keySignedHint') }}</p>
      <code>X-External-User-Token</code>
      <label>{{ $t('integrations.api.hmacSecret') }}</label>
      <p>{{ $t('integrations.api.hmacSecretDesc') }}</p>
      <t-input :model-value="modelValue.hmac_secret || ''" :type="showSecret ? 'text' : 'password'"
        :placeholder="hasSecret ? $t('integrations.api.secretConfigured') : ''"
        @change="patch({ hmac_secret: String($event) || undefined })" />
      <div class="identity-form__actions">
        <t-button size="small" variant="text" @click="generateSecret">{{ $t('integrations.api.generateSecret') }}</t-button>
        <t-button v-if="modelValue.hmac_secret" size="small" variant="text" @click="copySecret">{{ $t('integrations.api.copy') }}</t-button>
      </div>
      <p v-if="modelValue.hmac_secret">{{ $t('integrations.api.keySecretDraftHint') }}</p>
    </template>
    <p v-if="editing">{{ $t('integrations.api.keyModeChangeHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { APIPrincipalMode, UpdateAPIPrincipalConfigPayload } from '@/api/tenant'
import { copyWithToast } from '@/utils/clipboard'
const props = defineProps<{ modelValue: UpdateAPIPrincipalConfigPayload; hasSecret?: boolean; editing?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: UpdateAPIPrincipalConfigPayload] }>()
const showSecret = ref(false)
function patch(value: Partial<UpdateAPIPrincipalConfigPayload>) {
  emit('update:modelValue', { ...props.modelValue, ...value })
}
function generateSecret() {
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  patch({ hmac_secret: btoa(String.fromCharCode(...bytes)) })
  showSecret.value = true
}
function copySecret() { void copyWithToast(props.modelValue.hmac_secret || '', 'integrations.api.copySuccess') }
</script>

<style scoped lang="less">
.identity-form {
  display: flex; flex-direction: column; gap: 12px;
  border-top: 1px solid var(--td-component-stroke); padding-top: 20px;
  p { margin: 0; color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.6; }
  code { font-size: 12px; }
  &__title { font-weight: 600; }
  &__switch, &__actions { display: flex; align-items: center; gap: 12px; }
  &__switch { justify-content: space-between; }
}
</style>
