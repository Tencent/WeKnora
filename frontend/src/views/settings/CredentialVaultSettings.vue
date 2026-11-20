<!-- frontend/src/views/settings/CredentialVaultSettings.vue -->
<template>
  <div class="vault-settings">
    <div class="section-header">
      <h2>{{ t('credentialVault.title') }}</h2>
      <p class="section-description">{{ t('credentialVault.description') }}</p>
    </div>

    <t-alert v-if="!available && !loading" theme="warning" :message="t('credentialVault.unavailable')" />

    <div class="vault-toolbar">
      <t-button size="small" theme="primary" :disabled="!available" @click="openCreate">
        {{ t('credentialVault.add') }}
      </t-button>
    </div>

    <p v-if="loading" class="vault-state">{{ t('credentialVault.loading') }}</p>
    <p v-else-if="loadError" class="vault-state vault-state--error">{{ loadError }}</p>
    <p v-else-if="items.length === 0" class="vault-state">{{ t('credentialVault.empty') }}</p>

    <ul v-else class="vault-list">
      <li v-for="item in items" :key="item.id" class="vault-item">
        <div class="vault-item__main">
          <div class="vault-item__title">{{ item.title }}</div>
          <div class="vault-item__meta">
            {{ item.site }} · {{ item.username || '—' }} ·
            {{ item.last_used_at ? t('credentialVault.lastUsed', { time: formatTime(item.last_used_at) }) : t('credentialVault.never') }}
          </div>
          <div class="vault-item__domains">{{ t('credentialVault.domains') }}：{{ item.domains.join(', ') }}</div>
        </div>
        <div class="vault-item__actions">
          <t-button size="small" variant="text" :disabled="!available" @click="openEdit(item)">{{ t('credentialVault.edit') }}</t-button>
          <t-popconfirm :content="t('credentialVault.deleteConfirm')" @confirm="remove(item)">
            <t-button size="small" variant="text" theme="danger">{{ t('credentialVault.delete') }}</t-button>
          </t-popconfirm>
        </div>
      </li>
    </ul>

    <t-dialog v-model:visible="dialogVisible" :header="editing ? t('credentialVault.edit') : t('credentialVault.add')"
      :confirm-btn="{ content: t('credentialVault.save'), loading: saving }" @confirm="save" @close="resetForm">
      <t-form label-align="top" @submit.prevent>
        <t-form-item :label="t('credentialVault.titleLabel')">
          <t-input v-model="form.title" :maxlength="100" />
        </t-form-item>
        <t-form-item :label="t('credentialVault.site')">
          <t-input v-model="form.site" :disabled="Boolean(editing)" placeholder="accounts.google.com" />
        </t-form-item>
        <t-form-item :label="t('credentialVault.domains')" :help="t('credentialVault.domainsHint')">
          <t-input v-model="form.domains" placeholder="accounts.google.com, sso.example.com" />
        </t-form-item>
        <t-form-item :label="t('credentialVault.username')">
          <t-input v-model="form.username" autocomplete="off" :maxlength="256" />
        </t-form-item>
        <t-form-item :label="editing ? t('credentialVault.newPassword') : t('credentialVault.password')"
          :help="editing ? t('credentialVault.passwordHint') : ''">
          <t-input v-model="form.password" type="password" autocomplete="new-password" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  createCredentialVaultItem, deleteCredentialVaultItem, listCredentialVault, updateCredentialVaultItem,
  type CredentialVaultChanges,
} from '@/api/credentialVault'
import type { CredentialVaultItem } from '@/utils/secureForm'

const { t, locale } = useI18n()

const items = ref<CredentialVaultItem[]>([])
const available = ref(true)
const loading = ref(true)
const loadError = ref('')
const dialogVisible = ref(false)
const saving = ref(false)
const editing = ref<CredentialVaultItem | null>(null)
// The password is write-only: it lives here only while the dialog is open.
const form = reactive({ title: '', site: '', domains: '', username: '', password: '' })

const errorMessage = (e: any, fallback: string) =>
  e?.response?.data?.error?.message || e?.error?.message || e?.message || fallback

const formatTime = (iso: string) => new Date(iso).toLocaleString(locale.value)

const parseDomains = (raw: string) => raw.split(/[,\s]+/).map((d) => d.trim()).filter(Boolean)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await listCredentialVault()
    items.value = res.data
    available.value = res.available
  } catch (e) {
    loadError.value = errorMessage(e, t('credentialVault.loadFailed'))
  } finally {
    loading.value = false
  }
}

function resetForm() {
  Object.assign(form, { title: '', site: '', domains: '', username: '', password: '' })
  editing.value = null
}

function openCreate() {
  resetForm()
  dialogVisible.value = true
}

function openEdit(item: CredentialVaultItem) {
  resetForm()
  editing.value = item
  Object.assign(form, { title: item.title, site: item.site, domains: item.domains.join(', '), username: item.username })
  dialogVisible.value = true
}

async function save() {
  saving.value = true
  try {
    if (editing.value) {
      const changes: CredentialVaultChanges = {
        title: form.title, domains: parseDomains(form.domains), username: form.username,
      }
      if (form.password) changes.password = form.password
      await updateCredentialVaultItem(editing.value.id, changes)
    } else {
      await createCredentialVaultItem({
        title: form.title, site: form.site, domains: parseDomains(form.domains),
        username: form.username, password: form.password,
      })
    }
    MessagePlugin.success(t('credentialVault.saved'))
    dialogVisible.value = false
    resetForm()
    await load()
  } catch (e) {
    MessagePlugin.error(errorMessage(e, t('credentialVault.saveFailed')))
  } finally {
    form.password = ''
    saving.value = false
  }
}

async function remove(item: CredentialVaultItem) {
  try {
    await deleteCredentialVaultItem(item.id)
    MessagePlugin.success(t('credentialVault.deleted'))
    await load()
  } catch (e) {
    MessagePlugin.error(errorMessage(e, t('credentialVault.saveFailed')))
  }
}

onMounted(load)
</script>

<style scoped lang="less">
.vault-settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.vault-toolbar {
  display: flex;
  justify-content: flex-end;
}

.vault-state {
  color: var(--td-text-color-secondary);
}

.vault-state--error {
  color: var(--td-error-color);
}

.vault-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.vault-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md, 8px);
}

.vault-item__title {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.vault-item__meta,
.vault-item__domains {
  font-size: var(--app-text-xs, 12px);
  color: var(--td-text-color-secondary);
}

.vault-item__actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}
</style>
