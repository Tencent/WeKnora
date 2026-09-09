<template>
  <div class="kb-permissions">
    <t-radio-group :value="granular ? 'per-kb' : 'uniform'" @change="changeMode">
      <t-radio-button value="per-kb">{{ $t('integrations.api.kbPermissionPerKB') }}</t-radio-button>
      <t-radio-button value="uniform">{{ $t('integrations.api.kbPermissionUniform') }}</t-radio-button>
    </t-radio-group>
    <template v-if="granular">
      <p class="hint">{{ $t('integrations.api.kbPermissionHint') }}</p>
      <t-select
        :model-value="selectedIDs"
        multiple filterable clearable
        :loading="loading"
        :options="selectOptions"
        :placeholder="$t('integrations.api.kbPermissionSelect')"
        @change="changeSelection"
      />
      <p v-if="selectedIDs.length === 0" class="hint">{{ $t('integrations.api.kbPermissionEmpty') }}</p>
      <div v-for="id in selectedIDs" :key="id" class="kb-row">
        <div class="kb-name">
          <strong>{{ optionByID.get(id)?.name || id }}</strong>
          <span>{{ sourceLabel(id) }}</span>
        </div>
        <t-select
          :model-value="modelValue?.[id]"
          :aria-label="`${optionByID.get(id)?.name || id} ${$t('integrations.api.apiKeyKnowledgeScope')}`"
          :options="permissionOptions(id)"
          @change="(value: unknown) => changePermission(id, value)"
        />
      </div>
    </template>
    <template v-else>
      <t-select
        :model-value="legacyIds"
        multiple filterable clearable
        :loading="loading"
        :options="selectOptions.filter(option => !optionByID.get(option.value)?.shared)"
        :placeholder="$t('integrations.api.apiKeyKnowledgeScopePlaceholder')"
        @change="(value: unknown) => emit('update:legacyIds', value as string[])"
      />
      <p class="hint">{{ $t('integrations.api.kbPermissionUniformHint') }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { APIKeyKBPermission, APIKeyKBPermissions, TenantAPIKeyCapability } from '@/api/tenant'
import { API_KEY_KB_PERMISSIONS, canAssignKBPermission, selectAPIKeyKBs, type APIKeyKnowledgeBaseOption } from './apiKeyScope'

const props = defineProps<{
  modelValue: APIKeyKBPermissions | null
  legacyIds: string[]
  options: APIKeyKnowledgeBaseOption[]
  capabilities: TenantAPIKeyCapability[]
  loading: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [APIKeyKBPermissions | null]
  'update:legacyIds': [string[]]
}>()
const { t } = useI18n()
const granular = computed(() => props.modelValue !== null)
const selectedIDs = computed(() => Object.keys(props.modelValue || {}))
const optionByID = computed(() => new Map(props.options.map(option => [option.id, option])))
const selectOptions = computed(() => props.options.map(option => ({
  value: option.id,
  label: option.shared ? `${option.name} · ${option.source}` : option.name,
})))
const permissionLabels: Record<APIKeyKBPermission, string> = {
  read: 'integrations.api.kbPermissionRead',
  write: 'integrations.api.kbPermissionWrite',
  manage: 'integrations.api.kbPermissionManage',
}

function sourceLabel(id: string): string {
  const option = optionByID.value.get(id)
  if (!option) return t('integrations.api.kbPermissionUnavailable')
  return option.shared ? option.source || '' : t('integrations.api.kbPermissionOwn')
}

function permissionOptions(id: string) {
  return API_KEY_KB_PERMISSIONS.map(permission => ({
    value: permission,
    label: t(permissionLabels[permission]),
    disabled: !canAssignKBPermission(permission, optionByID.value.get(id), props.capabilities),
  }))
}

function changeSelection(value: unknown) {
  emit('update:modelValue', selectAPIKeyKBs(value as string[], props.modelValue || {}))
}

function changePermission(id: string, value: unknown) {
  const permission = value as APIKeyKBPermission
  if (canAssignKBPermission(permission, optionByID.value.get(id), props.capabilities)) {
    emit('update:modelValue', { ...props.modelValue, [id]: permission })
  }
}

function changeMode(value: unknown) {
  if (value === 'uniform') {
    // Shared KBs are available only in explicit per-KB mode.
    emit('update:legacyIds', selectedIDs.value.filter(id => optionByID.value.get(id)?.shared === false))
    emit('update:modelValue', null)
    return
  }
  // Switching an all-KB legacy key requires an explicit selection; never
  // silently include newly shared KBs or turn an empty map into all access.
  emit('update:modelValue', Object.fromEntries(props.legacyIds.map(id => {
    const maximum = [...API_KEY_KB_PERMISSIONS].reverse().find(permission =>
      canAssignKBPermission(permission, optionByID.value.get(id), props.capabilities),
    ) || 'read'
    return [id, maximum]
  })))
  emit('update:legacyIds', [])
}
</script>

<style scoped lang="less">
.kb-permissions { display: flex; flex-direction: column; gap: 12px; }
.hint { margin: 0; color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.6; }
.kb-row { display: flex; gap: 12px; align-items: center; }
.kb-name { display: flex; flex: 1; min-width: 0; flex-direction: column; gap: 4px; }
.kb-name strong { font-weight: 500; overflow-wrap: anywhere; }
.kb-name span { color: var(--td-text-color-secondary); font-size: 12px; overflow-wrap: anywhere; }
.kb-row > :last-child { width: 156px; flex: 0 0 156px; }
</style>
