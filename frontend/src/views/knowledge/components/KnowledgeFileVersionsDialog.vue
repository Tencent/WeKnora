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
}>();
const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'uploaded', knowledgeId: string): void;
}>();
const { t, locale } = useI18n();
const items = ref<KnowledgeFileVersion[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = 20;
const loading = ref(false);
const uploading = ref(false);
const error = ref('');
const currentVersion = ref<number>();
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
  if (nextPage === 1) currentVersion.value = undefined;
  try {
    const result = await listKnowledgeFileVersions(id, (nextPage - 1) * pageSize, pageSize);
    if (generation !== loadGeneration) return;
    if (!result.success) throw new Error();
    items.value = result.data.items;
    total.value = result.data.total;
    page.value = nextPage;
    const current = items.value.find(item => item.is_current);
    if (current) currentVersion.value = current.version;
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
  currentVersion.value = undefined;
  selectedFile.value = null;
  error.value = '';
  loading.value = false;
  if (fileInput.value) fileInput.value.value = '';
  if (props.visible) void loadPage();
}, { immediate: true });

function selectFile(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  selectedFile.value = null;
  if (!file) return;
  if (!file.size || file.size > MAX_FILE_SIZE_MB * 1024 * 1024) {
    MessagePlugin.warning(t('knowledgeBase.fileVersions.invalidSize', { size: MAX_FILE_SIZE_MB }));
    input.value = '';
    return;
  }
  selectedFile.value = file;
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
  } catch (err: any) {
    if (err?.response?.status === 409 || err?.status === 409 || err?.$httpStatus === 409) {
      MessagePlugin.warning(t('knowledgeBase.fileVersions.conflict'));
      await loadPage();
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
const close = () => { if (!uploading.value) emit('update:visible', false); };
</script>

<template>
  <t-dialog :visible="visible" :header="t('knowledgeBase.fileVersions.title')" width="720px" :footer="false"
    :close-btn="!uploading" :close-on-overlay-click="!uploading" :close-on-esc-keydown="!uploading" @close="close">
    <div class="version-history">
      <p class="document-name">{{ knowledge?.file_name || knowledge?.title }}</p>
      <section v-if="canUpload" class="upload-section">
        <p class="hint">{{ t('knowledgeBase.fileVersions.uploadHint') }}</p>
        <p v-if="processing" class="hint">{{ t('knowledgeBase.fileVersions.processing') }}</p>
        <div class="upload-actions">
          <input ref="fileInput" class="file-input" type="file" :disabled="processing || uploading"
            :aria-label="t('knowledgeBase.fileVersions.selectFile')" @change="selectFile" />
          <t-button variant="outline" :disabled="processing || uploading" @click="fileInput?.click()">
            {{ t('knowledgeBase.fileVersions.selectFile') }}
          </t-button>
          <span v-if="selectedFile" class="selected-file" :title="selectedFile.name">{{ selectedFile.name }}</span>
          <t-button theme="primary" :loading="uploading" :disabled="!canSubmit" @click="upload">
            {{ t('knowledgeBase.fileVersions.upload') }}
          </t-button>
        </div>
      </section>
      <div v-if="error" class="load-error" role="alert">
        {{ error }}
        <t-button variant="text" @click="loadPage(page)">{{ t('common.retry') }}</t-button>
      </div>
      <t-loading :loading="loading" class="history-loading">
        <ul v-if="items.length" class="version-list">
          <li v-for="version in items" :key="version.version" class="version-row">
            <div class="version-meta">
              <div class="version-heading">
                <strong>v{{ version.version }}</strong>
                <t-tag v-if="version.is_current" size="small" theme="primary" variant="light">
                  {{ t('knowledgeBase.fileVersions.current') }}
                </t-tag>
              </div>
              <div class="version-name" :title="version.file_name">{{ version.file_name }}</div>
              <div class="hint">{{ formatDate(version.created_at) }} · {{ formatFileSize(version.file_size) }}</div>
            </div>
            <t-button v-if="canDownload" variant="text" :loading="downloading === version.version"
              :disabled="downloading !== undefined" @click="download(version)">
              {{ t('common.download') }}
            </t-button>
          </li>
        </ul>
        <p v-else-if="!loading && !error" class="hint">{{ t('knowledgeBase.fileVersions.empty') }}</p>
      </t-loading>
      <t-pagination v-if="total > pageSize" :current="page" :page-size="pageSize" :total="total"
        :show-page-size="false" :disabled="loading || uploading" size="small" @current-change="loadPage" />
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.version-history { display: flex; flex-direction: column; gap: 16px; }
.document-name { margin: 0; font-weight: 600; overflow-wrap: anywhere; }
.upload-section { padding: 16px; border-radius: var(--app-radius-md); background: var(--td-bg-color-secondarycontainer); }
.hint { color: var(--td-text-color-secondary); font-size: var(--app-text-sm); margin: 0; }
.upload-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; margin-top: 12px; }
.file-input { display: none; }
.selected-file { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.load-error { color: var(--td-error-color); }
.history-loading { min-height: 70px; }
.version-list { list-style: none; margin: 0; padding: 0; max-height: 420px; overflow: auto; }
.version-row { display: flex; align-items: center; gap: 16px; padding: 14px 0; border-bottom: 1px solid var(--td-component-stroke); }
.version-meta { flex: 1; min-width: 0; }
.version-heading { display: flex; align-items: center; gap: 10px; }
.version-name { margin: 5px 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
