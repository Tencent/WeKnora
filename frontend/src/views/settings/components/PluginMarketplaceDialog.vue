<template>
  <SettingDrawer v-model:visible="drawerVisible" :title="$t('settings.plugins.marketplaceDialog.title')"
    :description="$t('settings.plugins.marketplaceDialog.description')" icon="app" width="560px" :min-width="480"
    :max-width="760" storage-key="setting-drawer:width:plugin-marketplace-add" :confirm-loading="submitting"
    :confirm-disabled="!canSubmit" :confirm-text="$t('settings.plugins.marketplaceDialog.submit')" @confirm="submit">
    <section class="setting-drawer__section">
      <t-radio-group v-model="mode" variant="default-filled" :disabled="submitting">
        <t-radio-button value="folder">{{ $t('settings.plugins.marketplaceDialog.sourceFolder') }}</t-radio-button>
        <t-radio-button value="archive">{{ $t('settings.plugins.marketplaceDialog.sourceArchive') }}</t-radio-button>
        <t-radio-button value="git">{{ $t('settings.plugins.marketplaceDialog.sourceGit') }}</t-radio-button>
      </t-radio-group>
    </section>

    <section v-if="mode === 'folder'" class="setting-drawer__section">
      <input ref="folderInput" type="file" class="marketplace-dialog__file" webkitdirectory multiple
        data-testid="folder-input" @change="onFolderPicked">
      <t-button variant="outline" :disabled="submitting" @click="folderInput?.click()">
        <template #icon><t-icon name="folder-open" /></template>
        {{ $t('settings.plugins.marketplaceDialog.pickFolder') }}
      </t-button>
      <p v-if="folderName" class="marketplace-dialog__picked">
        {{ $t('settings.plugins.marketplaceDialog.selected', { name: folderName }) }}
      </p>
      <p class="marketplace-dialog__hint">{{ $t('settings.plugins.marketplaceDialog.folderHint') }}</p>
    </section>

    <section v-else-if="mode === 'archive'" class="setting-drawer__section">
      <input ref="archiveInput" type="file" class="marketplace-dialog__file" :accept="MARKETPLACE_ARCHIVE_ACCEPT"
        data-testid="archive-input" @change="onArchivePicked">
      <t-button variant="outline" :disabled="submitting" @click="archiveInput?.click()">
        <template #icon><t-icon name="upload" /></template>
        {{ $t('settings.plugins.marketplaceDialog.pickArchive') }}
      </t-button>
      <p v-if="archive" class="marketplace-dialog__picked">
        {{ $t('settings.plugins.marketplaceDialog.selected', { name: archive.name }) }}
      </p>
      <p class="marketplace-dialog__hint">{{ $t('settings.plugins.marketplaceDialog.archiveHint') }}</p>
    </section>

    <section v-else class="setting-drawer__section">
      <t-input v-model="source" :placeholder="$t('settings.plugins.marketplaceDialog.gitPlaceholder')"
        :disabled="submitting" clearable @enter="submit" />
      <p class="marketplace-dialog__hint">{{ $t('settings.plugins.marketplaceDialog.gitHint') }}</p>
    </section>

    <t-progress v-if="submitting && mode !== 'git' && uploadPercent > 0" :percentage="uploadPercent" size="small" />
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  addPluginMarketplaceFromFile,
  addPluginMarketplaceFromSource,
  type PluginMarketplace,
} from '@/api/plugin'
import {
  folderHasMarketplaceManifest,
  isMarketplaceArchiveName,
  isMarketplaceSource,
  MARKETPLACE_ARCHIVE_ACCEPT,
  zipMarketplaceFolder,
  type FolderFile,
} from '@/utils/pluginMarketplace'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; added: [marketplace: PluginMarketplace] }>()
const { t } = useI18n()

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const mode = ref<'folder' | 'archive' | 'git'>('folder')
const folderInput = ref<HTMLInputElement | null>(null)
const archiveInput = ref<HTMLInputElement | null>(null)
const folderFiles = ref<FolderFile[]>([])
const folderName = ref('')
const archive = ref<File | null>(null)
const source = ref('')
const submitting = ref(false)
const uploadPercent = ref(0)

const canSubmit = computed(() => {
  if (submitting.value) return false
  if (mode.value === 'folder') return folderFiles.value.length > 0
  if (mode.value === 'archive') return archive.value !== null
  return isMarketplaceSource(source.value)
})

watch(() => props.visible, (open) => {
  if (!open) return
  folderFiles.value = []
  folderName.value = ''
  archive.value = null
  source.value = ''
  uploadPercent.value = 0
})

function onFolderPicked(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  const picked = files.map((file) => ({ path: file.webkitRelativePath || file.name, data: file }))
  if (!folderHasMarketplaceManifest(picked.map((f) => f.path))) {
    MessagePlugin.error(t('settings.plugins.marketplaceDialog.noManifest'))
    return
  }
  folderFiles.value = picked
  folderName.value = picked[0]?.path.split('/')[0] || ''
}

function onArchivePicked(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0] || null
  input.value = ''
  if (file && !isMarketplaceArchiveName(file.name)) {
    MessagePlugin.error(t('settings.plugins.marketplaceDialog.invalidArchive'))
    return
  }
  archive.value = file
}

function showImportWarnings(warnings: string[]) {
  const groups = [
    { marker: '仓库外', key: 'settings.plugins.marketplaceDialog.skippedOutside' },
    { marker: '没有可安装的技能或 MCP', key: 'settings.plugins.marketplaceDialog.skippedNoPayload' },
  ]
  let rest = warnings
  for (const group of groups) {
    const matched = rest.filter((warning) => warning.includes(group.marker))
    rest = rest.filter((warning) => !warning.includes(group.marker))
    if (matched.length === 1) MessagePlugin.warning(matched[0])
    else if (matched.length > 1) MessagePlugin.warning(t(group.key, { count: matched.length }))
  }
  for (const warning of rest) MessagePlugin.warning(warning)
}

async function submit() {
  if (!canSubmit.value) {
    if (mode.value === 'git' && source.value.trim()) {
      MessagePlugin.error(t('settings.plugins.marketplaceDialog.invalidSource'))
    }
    return
  }
  submitting.value = true
  uploadPercent.value = 0
  const onProgress = (percent: number) => { uploadPercent.value = percent }
  try {
    let res: { data: PluginMarketplace }
    if (mode.value === 'git') {
      res = await addPluginMarketplaceFromSource(source.value.trim())
    } else if (mode.value === 'archive' && archive.value) {
      res = await addPluginMarketplaceFromFile(archive.value, undefined, onProgress)
    } else {
      const blob = await zipMarketplaceFolder(folderFiles.value)
      res = await addPluginMarketplaceFromFile(blob, `${folderName.value || 'marketplace'}.zip`, onProgress)
    }
    MessagePlugin.success(t('settings.plugins.toasts.marketplaceAdded', { name: res?.data?.name || '' }))
    showImportWarnings(res?.data?.import_warnings || [])
    emit('added', res.data)
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('settings.plugins.toasts.marketplaceAddFailed'))
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped lang="less">
.marketplace-dialog__file {
  display: none;
}

.marketplace-dialog__picked {
  margin: 8px 0 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
}

.marketplace-dialog__hint {
  margin: 8px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}
</style>
