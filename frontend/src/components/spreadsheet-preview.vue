<script setup lang="ts">
import { ref, shallowRef, watch, onUnmounted, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { PREVIEW_ROWS, PREVIEW_COLUMNS, PREVIEW_MAX_BYTES, PREVIEW_TIMEOUT_MS, type SpreadsheetSheet, type SpreadsheetPage } from '@/utils/spreadsheetPreviewLimits';

const props = defineProps<{ blob: Blob; fileType?: string; active?: boolean }>();
const { t } = useI18n();
const sheets = shallowRef<SpreadsheetSheet[]>([]);
const page = shallowRef<SpreadsheetPage | null>(null);
const selectedSheet = ref(0);
const loading = ref(true);
const error = ref('');
const errorMessage = computed(() => {
  if (error.value === 'preview.spreadsheet.tooLarge') {
    return t('preview.spreadsheet.tooLarge', { size: PREVIEW_MAX_BYTES / 1024 / 1024 });
  }
  if (error.value === 'preview.spreadsheet.timeout') return t('preview.spreadsheet.timeout');
  return error.value ? t('preview.loadFailed') : '';
});
let worker: Worker | null = null;
let requestId = 0;
let requestTimer: ReturnType<typeof setTimeout> | undefined;
const currentSheet = computed(() => sheets.value[selectedSheet.value]);

function clearRequestTimer() {
  clearTimeout(requestTimer);
  requestTimer = undefined;
}

function startRequestTimer(id: number) {
  clearRequestTimer();
  requestTimer = setTimeout(() => {
    if (id !== requestId) return;
    error.value = 'preview.spreadsheet.timeout';
    loading.value = false;
    dispose();
  }, PREVIEW_TIMEOUT_MS);
}

function dispose() {
  clearRequestTimer();
  ++requestId;
  worker?.terminate();
  worker = null;
}

async function load() {
  dispose();
  const id = ++requestId;
  loading.value = true;
  error.value = '';
  page.value = null;
  sheets.value = [];
  selectedSheet.value = 0;
  if (props.active === false) return;
  if (props.blob.size > PREVIEW_MAX_BYTES) {
    error.value = 'preview.spreadsheet.tooLarge';
    loading.value = false;
    return;
  }
  startRequestTimer(id);
  try {
    const buffer = await props.blob.arrayBuffer();
    if (id !== requestId) return;
    const activeWorker = new Worker(new URL('../workers/spreadsheetPreview.worker.ts', import.meta.url), { type: 'module' });
    worker = activeWorker;
    worker.onmessage = ({ data }) => {
      if (data.id !== requestId) return;
      clearRequestTimer();
      loading.value = false;
      if (data.error) {
        error.value = 'preview.loadFailed';
        dispose();
        return;
      }
      sheets.value = data.sheets;
      selectedSheet.value = data.sheetIndex;
      page.value = data.page;
    };
    worker.onerror = () => {
      if (worker !== activeWorker) return;
      loading.value = false;
      error.value = 'preview.loadFailed';
      dispose();
    };
    worker.postMessage({ type: 'load', id, buffer, fileType: props.fileType }, [buffer]);
  } catch (err) {
    if (id !== requestId) return;
    loading.value = false;
    error.value = 'preview.loadFailed';
    dispose();
  }
}

function navigate(rowPage = 1, columnPage = 1) {
  if (!worker) return;
  loading.value = true;
  const id = ++requestId;
  startRequestTimer(id);
  worker.postMessage({ type: 'page', id, sheetIndex: selectedSheet.value, page: rowPage, columnPage });
}

watch(() => [props.blob, props.fileType, props.active], load, { immediate: true });
onUnmounted(dispose);
</script>

<template>
  <div class="spreadsheet-preview" :aria-busy="loading">
    <div v-if="error" role="alert">{{ errorMessage }} <t-button size="small" variant="outline" @click="load">{{ t('preview.retry') }}</t-button></div>
    <div v-else>
      <div class="sheet-controls" v-if="sheets.length">
        <label>{{ t('preview.spreadsheet.worksheet') }}
          <select v-model="selectedSheet" :disabled="loading" @change="navigate()">
            <option v-for="(sheet, index) in sheets" :key="index" :value="index">{{ sheet.name }}</option>
          </select>
        </label>
        <span>{{ t('preview.spreadsheet.dimensions', { rows: currentSheet?.rows || 0, columns: currentSheet?.columns || 0 }) }}</span>
      </div>
      <div v-if="loading" role="status">{{ t('preview.loading') }}</div>
      <template v-if="page">
        <p class="sheet-hint">{{ t('preview.spreadsheet.valuesOnly') }}</p>
        <p v-if="page.truncated" class="sheet-hint">{{ t('preview.spreadsheet.truncated') }}</p>
        <div class="sheet-controls">
          <t-pagination :current="page.page" :total="currentSheet?.rows || 0" :page-size="PREVIEW_ROWS"
            size="small" show-jumper :show-page-size="false" :disabled="loading" @change="(value: { current: number }) => navigate(value.current, page!.columnPage)" />
          <template v-if="(currentSheet?.columns || 0) > PREVIEW_COLUMNS">
            <t-button size="small" variant="outline" :disabled="loading || page.columnPage <= 1" @click="navigate(page.page, page.columnPage - 1)">{{ t('preview.spreadsheet.previousColumns') }}</t-button>
            <span>{{ page.columns[0] }}–{{ page.columns.at(-1) }}</span>
            <t-button size="small" variant="outline" :disabled="loading || page.columnPage * PREVIEW_COLUMNS >= (currentSheet?.columns || 0)" @click="navigate(page.page, page.columnPage + 1)">{{ t('preview.spreadsheet.nextColumns') }}</t-button>
          </template>
        </div>
        <div class="sheet-table-scroll" tabindex="0" :aria-label="t('preview.spreadsheet.contents')">
          <table>
            <thead><tr><th scope="col">{{ t('preview.spreadsheet.rowNumber') }}</th><th v-for="col in page.columns" :key="col" scope="col">{{ col }}</th></tr></thead>
            <tbody><tr v-for="row in page.rows" :key="row.number"><th scope="row">{{ row.number }}</th><td v-for="(cell, index) in row.cells" :key="index">{{ cell }}</td></tr></tbody>
          </table>
          <p v-if="!page.rows.length">{{ t('preview.spreadsheet.empty') }}</p>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.sheet-controls { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
select { max-width: 320px; padding: 6px; color: inherit; background: var(--td-bg-color-container); }
.sheet-hint { color: var(--td-text-color-secondary); font-size: var(--app-text-xs); margin-bottom: 12px; }
.sheet-table-scroll { overflow: auto; max-height: 65vh; }
table { border-collapse: collapse; font-size: var(--app-text-sm); }
th, td { border: 1px solid var(--td-component-border); padding: 6px 10px; min-width: 80px; max-width: 360px; white-space: pre-wrap; overflow-wrap: anywhere; }
thead th { position: sticky; top: 0; background: var(--td-bg-color-secondarycontainer); }
tbody th { background: var(--td-bg-color-secondarycontainer); min-width: 44px; }
</style>
