<template>
  <section class="browser-search-preferences" :aria-label="t('localBrowser.searchInstructionsTitle')">
    <h3>{{ t('localBrowser.searchInstructionsTitle') }}</h3>
    <p class="description">{{ t('localBrowser.searchInstructionsDescription') }}</p>
    <t-skeleton v-if="loading" animation="gradient" :row-col="[{ width: '100%', height: '64px' }]" />
    <template v-else-if="loaded">
      <t-textarea v-model="draft" :autosize="{ minRows: 2, maxRows: 8 }" :maxlength="4000"
        :tips="t('localBrowser.searchInstructionsHint')"
        :disabled="saving" :aria-label="t('localBrowser.searchInstructionsTitle')" />
      <div class="actions">
        <span v-if="saved && !dirty" role="status">{{ t('localBrowser.searchInstructionsSaved') }}</span>
        <t-button size="small" theme="default" variant="text" :disabled="saving || draft === defaultInstructions"
          @click="restoreDefault">{{ t('localBrowser.searchInstructionsReset') }}</t-button>
        <t-button size="small" theme="primary" :loading="saving" :disabled="!dirty || saving" @click="save">
          {{ t('common.save') }}
        </t-button>
      </div>
    </template>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <t-button v-if="!loading && !loaded" size="small" theme="default" variant="outline" @click="load">
      {{ t('common.retry') }}
    </t-button>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCurrentUser, updateMyPreferences } from '@/api/auth'

const { t } = useI18n()
const draft = ref(''), defaultInstructions = ref(''), persisted = ref('')
const loading = ref(false), loaded = ref(false), saving = ref(false), saved = ref(false), error = ref('')
const dirty = computed(() => draft.value.trim() !== persisted.value.trim())
let alive = true

async function load() {
  if (loading.value || saving.value) return
  loading.value = true; error.value = ''
  try {
    const result = await getCurrentUser()
    if (!alive) return
    const defaults = result.data?.preference_defaults?.browser_search_instructions
    if (!result.success || !result.data || !defaults) throw new Error(result.message || t('localBrowser.failed'))
    defaultInstructions.value = defaults
    persisted.value = result.data.user.preferences?.browser_search_instructions?.trim() || defaults
    draft.value = persisted.value
    loaded.value = true
  } catch (e: any) { if (alive) error.value = e?.message || t('localBrowser.failed') }
  finally { if (alive) loading.value = false }
}

function restoreDefault() { draft.value = defaultInstructions.value; saved.value = false }

async function save() {
  if (!loaded.value || saving.value || !dirty.value) return
  saving.value = true; saved.value = false; error.value = ''
  const text = draft.value.trim()
  try {
    const result = await updateMyPreferences({
      browser_search_instructions: text === defaultInstructions.value ? '' : text,
    })
    if (!alive) return
    if (!result.success || !result.data) throw new Error(result.message || t('localBrowser.failed'))
    persisted.value = result.data.browser_search_instructions?.trim() || defaultInstructions.value
    draft.value = persisted.value
    saved.value = true
  } catch (e: any) { if (alive) error.value = e?.message || t('localBrowser.failed') }
  finally { if (alive) saving.value = false }
}

onMounted(() => { void load() })
onBeforeUnmount(() => { alive = false })
</script>

<style scoped lang="less">
.browser-search-preferences {
  margin-top: 20px;
  h3 { margin: 0 0 6px; font-size: 16px; color: var(--td-text-color-primary); }
  .description { margin: 0 0 8px; color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.5; }
  .actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; margin-top: 8px; }
  .actions span { color: var(--td-success-color); font-size: 12px; }
  :deep(.t-textarea__inner) { font-size: 13px; line-height: 20px; }
  :deep(.t-textarea__info_wrapper) { font-size: 12px; }
  .error { color: var(--td-error-color); font-size: 13px; }
}
</style>
