<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  downloadKnowledgeFileVersion,
  listKnowledgeFileVersions,
  uploadKnowledgeFileVersion,
  type KnowledgeFileVersion,
} from '@/api/knowledge-base';
import { formatFileSize } from '@/utils/files';
import { MAX_FILE_SIZE_MB } from '@/utils';
import { isKnowledgeParseInFlight } from '../wikiStatusRefresh';

const props = defineProps<{
  visible: boolean;
  knowledge: { id: string; file_name?: string; title?: string; parse_status?: string } | null;
  canUpload: boolean;
  canDownload: boolean;
  initialFile?: File | null;
}>();
const emit = defineEmits<{
  (e: 'uploaded', knowledgeId: string): void;
  (e: 'file-consumed'): void;
}>();
const { t, locale } = useI18n();
const items = ref<KnowledgeFileVersion[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = 20;
const loading = ref(false);
const uploading = ref(false);
const error = ref('');
const currentFile = ref<KnowledgeFileVersion>();
const currentVersion = computed(() => currentFile.value?.version);
const displayedVersions = computed(() => page.value === 1 ? items.value : items.value.filter(item => !item.is_current));
const selectedFile = ref<File | null>(null);
const fileInput = ref<HTMLInputElement>();
const downloading = ref<number>();
let loadGeneration = 0;
const processing = computed(() => isKnowledgeParseInFlight(props.knowledge?.parse_status));
const canSubmit = computed(() => props.canUpload && !processing.value && !loading.value &&
  !uploading.value && !!selectedFile.value && currentVersion.value !== undefined && !error.value);

async function loadPage(nextPage = 1) {
  const id = props.knowledge?.id;
  if (!id || !props.visible) return;
  const generation = ++loadGeneration;
  loading.value = true;
  error.value = '';
  try {
    const result = await listKnowledgeFileVersions(id, (nextPage - 1) * pageSize, pageSize);
    if (generation !== loadGeneration) return;
    if (!result.success) throw new Error();
    items.value = result.data.items;
    total.value = result.data.total;
    page.value = nextPage;
    const current = items.value.find(item => item.is_current);
    if (current) currentFile.value = current;
  } catch {
    if (generation === loadGeneration) error.value = t('knowledgeBase.fileVersions.loadFailed');
  } finally {
    if (generation === loadGeneration) loading.value = false;
  }
}

watch(() => [props.visible, props.knowledge?.id] as const, () => {
  ++loadGeneration;
  items.value = [];
  total.value = 0;
  page.value = 1;
  currentFile.value = undefined;
  selectedFile.value = null;
  error.value = '';
  loading.value = false;
  if (fileInput.value) fileInput.value.value = '';
  if (props.visible) void loadPage();
}, { immediate: true });

watch(() => [props.visible, props.knowledge?.id, props.initialFile], () => {
  if (!props.visible || !props.knowledge?.id || !props.initialFile) return;
  acceptFile(props.initialFile);
  emit('file-consumed');
}, { immediate: true });

function selectFile(event: Event) {
  const input = event.target as HTMLInputElement;
  acceptFile(input.files?.[0]);
  input.value = '';
}

function acceptFile(file?: File) {
  if (!props.canUpload || processing.value || uploading.value) return;
  selectedFile.value = null;
  if (!file) return;
  if (!file.size || file.size > MAX_FILE_SIZE_MB * 1024 * 1024) {
    MessagePlugin.warning(t('knowledgeBase.fileVersions.invalidSize', { size: MAX_FILE_SIZE_MB }));
    return;
  }
  selectedFile.value = file;
}

function cancelUpload() {
  selectedFile.value = null;
}

async function upload() {
  if (!canSubmit.value || !props.knowledge || !selectedFile.value || currentVersion.value === undefined) return;
  const knowledgeId = props.knowledge.id;
  uploading.value = true;
  try {
    const result = await uploadKnowledgeFileVersion(knowledgeId, selectedFile.value, currentVersion.value);
    if (!result.success) throw new Error();
    MessagePlugin.success(t('knowledgeBase.fileVersions.uploaded'));
    emit('uploaded', knowledgeId);
    if (props.knowledge?.id === knowledgeId) {
      cancelUpload();
      await loadPage();
    }
  } catch (err: any) {
    if (err?.response?.status === 409 || err?.status === 409 || err?.$httpStatus === 409) {
      MessagePlugin.warning(t('knowledgeBase.fileVersions.conflict'));
      if (props.knowledge?.id === knowledgeId) await loadPage();
    } else {
      MessagePlugin.error(err?.message || t('knowledgeBase.fileVersions.uploadFailed'));
    }
  } finally {
    uploading.value = false;
  }
}

async function download(version: KnowledgeFileVersion) {
  if (!props.canDownload || downloading.value !== undefined) return;
  downloading.value = version.version;
  try {
    const file = await downloadKnowledgeFileVersion(version.knowledge_id, version.version);
    const url = URL.createObjectURL(file);
    const link = document.createElement('a');
    link.href = url;
    link.download = version.file_name;
    link.style.display = 'none';
    document.body.appendChild(link);
    link.click();
    nextTick(() => { link.remove(); URL.revokeObjectURL(url); });
  } catch {
    MessagePlugin.error(t('file.downloadFailed'));
  } finally {
    downloading.value = undefined;
  }
}

const formatDate = (value: string) => new Date(value).toLocaleString(locale.value);
</script>

<template>
  <div class="version-popover">
    <div class="popover-heading">
      <strong>{{ t('knowledgeBase.fileVersions.title') }}</strong>
      <span v-if="total" class="version-count">{{ total }}</span>
    </div>
    <div v-if="error" class="load-error" role="alert">
      {{ error }}<t-button size="small" variant="text" @click="loadPage(page)">{{ t('common.retry') }}</t-button>
    </div>
    <t-loading :loading="loading" class="history-loading">
      <ul v-if="displayedVersions.length" class="version-list">
        <li v-for="version in displayedVersions" :key="version.version" class="version-row">
          <div class="version-meta">
            <div class="version-heading">
              <strong>v{{ version.version }}</strong>
              <span v-if="version.is_current" class="current-label">{{ t('knowledgeBase.fileVersions.current') }}</span>
            </div>
            <div class="version-date" :title="version.file_name">{{ formatDate(version.created_at) }}</div>
          </div>
          <span class="version-size">{{ formatFileSize(version.file_size) }}</span>
          <t-button v-if="canDownload" class="version-download" theme="default" variant="text" shape="square" size="small"
            :title="t('common.download') + ' · v' + version.version"
            :aria-label="t('common.download') + ' · v' + version.version"
            :loading="downloading === version.version" :disabled="downloading !== undefined" @click="download(version)">
            <template #icon><t-icon name="download" size="16px" /></template>
          </t-button>
        </li>
      </ul>
      <p v-else-if="!loading && !error" class="empty-history">{{ t('knowledgeBase.fileVersions.empty') }}</p>
    </t-loading>
    <div v-if="total > pageSize" class="version-pagination">
      <t-pagination :current="page" :page-size="pageSize" :total="total" :show-page-size="false"
        :disabled="loading || uploading" size="small" theme="simple" @current-change="loadPage" />
    </div>
    <div v-if="canUpload" class="version-footer">
      <p v-if="processing" class="processing-notice" role="status"><t-icon name="time" size="14px" />{{ t('knowledgeBase.fileVersions.processing') }}</p>
      <input ref="fileInput" class="file-input" type="file" :disabled="processing || uploading"
        :aria-label="t('knowledgeBase.fileVersions.selectFile')" @change="selectFile" />
      <template v-if="selectedFile">
        <div class="selected-file"><t-icon name="file" size="16px" /><span :title="selectedFile.name">{{ selectedFile.name }}</span></div>
        <p class="upload-hint">{{ t('knowledgeBase.fileVersions.uploadHint') }}</p>
        <div class="upload-actions">
          <t-button theme="default" variant="text" size="small" :disabled="uploading" @click="cancelUpload">{{ t('common.cancel') }}</t-button>
          <t-button theme="primary" size="small" :loading="uploading" :disabled="!canSubmit" @click="upload">{{ t('knowledgeBase.fileVersions.confirmUpload') }}</t-button>
        </div>
      </template>
      <t-button v-else theme="default" variant="text" block :disabled="processing || uploading || loading || !!error" @click="fileInput?.click()">
        <template #icon><t-icon name="upload" size="15px" /></template>
        {{ t('knowledgeBase.fileVersions.upload') }}
      </t-button>
    </div>
  </div>
</template>

<style scoped lang="less">
.version-popover { width: 320px; max-width: calc(100vw - 32px); color: var(--td-text-color-primary); }
.popover-heading { display: flex; align-items: center; gap: 8px; padding: 14px 16px 10px; font-size: var(--app-text-md); }
.popover-heading strong { font-weight: 600; }
.version-count { padding: 0 6px; border-radius: 9px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: var(--app-text-xs); }
.history-loading { min-height: 55px; width: 100%; }
.version-list { list-style: none; margin: 0; padding: 0 6px 6px; max-height: 320px; overflow-y: auto; }
.version-row { display: grid; grid-template-columns: minmax(0, 1fr) 64px 24px; align-items: center; column-gap: 12px; padding: 10px; border-radius: 5px; }
.version-row:hover { background: var(--td-bg-color-container-hover); }
.version-meta { flex: 1; min-width: 0; }
.version-heading { display: flex; align-items: center; gap: 8px; font-size: var(--app-text-sm); }
.version-heading strong { font-weight: 600; font-variant-numeric: tabular-nums; }
.current-label { font-size: var(--app-text-2xs); font-weight: 500; padding: 0 5px; line-height: 18px; border-radius: 3px; color: var(--td-text-color-secondary); background: var(--td-bg-color-secondarycontainer); }
.version-size { text-align: right; white-space: nowrap; font-size: var(--app-text-xs); font-variant-numeric: tabular-nums; color: var(--td-text-color-secondary); }
.version-download { justify-self: end; }
.version-date { margin-top: 4px; font-size: var(--app-text-xs); line-height: 18px; color: var(--td-text-color-secondary); font-variant-numeric: tabular-nums; }
.version-footer { padding: 8px; border-top: 1px solid var(--td-component-stroke); }
.file-input { display: none; }
.selected-file { display: flex; align-items: center; gap: 6px; padding: 4px 6px; font-size: var(--app-text-sm); }
.selected-file span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.selected-file .t-icon { flex-shrink: 0; color: var(--td-text-color-secondary); }
.upload-hint { margin: 5px 6px 10px; font-size: var(--app-text-xs); line-height: 1.6; color: var(--td-text-color-secondary); }
.upload-actions { display: flex; justify-content: flex-end; gap: 6px; }
.processing-notice { display: flex; align-items: flex-start; gap: 6px; margin: 4px 6px 8px; font-size: var(--app-text-xs); line-height: 1.6; color: var(--td-text-color-secondary); }
.processing-notice .t-icon { flex-shrink: 0; margin-top: 2px; }
.version-pagination { padding: 6px 16px 12px; }
.load-error { padding: 8px 16px; color: var(--td-error-color); font-size: var(--app-text-sm); }
.empty-history { padding: 12px 16px 20px; margin: 0; font-size: var(--app-text-sm); color: var(--td-text-color-placeholder); }
</style>
