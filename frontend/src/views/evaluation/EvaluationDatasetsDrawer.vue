<template>
  <SettingDrawer :visible="visible" :title="t('evaluationFlow.datasets')" :description="t('evaluationFlow.importHint')" width="680px" :min-width="320" :resizable="false" :close-on-esc-keydown="!submitting" hide-footer @update:visible="close">
    <template #headerIcon><Database size="18px" aria-hidden="true" /></template>
    <template #header-actions><t-button theme="default" variant="text" shape="square" :aria-label="t('common.close')" :disabled="submitting" @click="close(false)"><X size="18px" aria-hidden="true" /></t-button></template>
    <div class="evaluation-flow">
      <nav class="flow-tabs" :aria-label="t('evaluationFlow.datasets')">
        <button v-for="tab in tabs" :key="tab.id" type="button" :aria-pressed="mode === tab.id" :disabled="submitting" @click="mode = tab.id">{{ tab.label }}</button>
      </nav>
      <div v-if="loading" class="flow-notice" role="status"><LoaderCircle size="16px" class="spin" />{{ t('evaluation.loading') }}</div>
      <div v-if="loadError" class="flow-error" role="alert">{{ loadError }}<button type="button" class="flow-link" @click="loadResources">{{ t('evaluation.refresh') }}</button></div>
      <div v-if="mode === 'library'" class="flow-stack">
        <p v-if="!datasets.length && !loading" class="flow-muted">{{ t('evaluationFlow.noDatasets') }}</p>
        <article v-for="dataset in datasets" :key="dataset.id" class="flow-card">
          <div class="flow-row"><h3>{{ dataset.name }}</h3><span class="flow-badge">{{ t(dataset.scope === 'system' ? 'evaluationFlow.system' : 'evaluationFlow.tenant') }}</span></div>
          <p>{{ dataset.description }}</p>
          <div class="flow-actions">
            <button type="button" class="flow-link" @click="inspectVersions(dataset)">{{ t('evaluationFlow.versions') }}</button>
            <button v-if="canManage && dataset.scope === 'tenant'" type="button" class="flow-link" @click="addVersion(dataset)"><Plus size="14px" />{{ t('evaluationFlow.addVersion') }}</button>
            <button v-if="canManage && dataset.current_version_id" type="button" class="flow-link" @click="emit('create', dataset.id, dataset.current_version_id)">{{ t('evaluationFlow.newRun') }}<ArrowRight size="14px" /></button>
          </div>
          <div v-if="inspectedId === dataset.id" class="version-list">
            <p v-if="versionsLoading" role="status">{{ t('evaluation.loading') }}</p>
            <p v-if="versionsError" class="flow-error" role="alert">{{ versionsError }}</p>
            <p v-if="!versionsLoading && !versions.length && !versionsError">{{ t('evaluationFlow.noVersions') }}</p>
            <div v-for="version in versions" :key="version.id" class="version-row"><span>v{{ version.version_number }} · {{ t('evaluationFlow.questionCount', { count: version.question_count }) }}</span><button v-if="canManage" type="button" class="flow-link" @click="emit('create', dataset.id, version.id)">{{ t('evaluationFlow.newRun') }}</button></div>
          </div>
        </article>
      </div>
      <template v-else>
        <div v-if="mode === 'catalog' && !content" class="flow-stack">
          <p class="flow-muted">{{ t('evaluationFlow.publicHint') }}</p>
          <article v-for="item in catalog" :key="item.id" class="flow-card">
            <div class="flow-row"><h3>{{ item.name }}</h3><span class="flow-badge">{{ item.language }}</span></div>
            <p>{{ item.description }}</p>
            <div class="flow-actions"><span class="flow-badge">{{ item.license }}</span><a v-if="safeSource(item.source_url)" :href="safeSource(item.source_url)" target="_blank" rel="noopener noreferrer" class="flow-link">{{ t('evaluationFlow.source') }}<ExternalLink size="13px" /></a></div>
            <small>{{ t('evaluationFlow.runScale', { passages: item.counts.passages ?? '—', questions: item.counts.questions ?? '—' }) }}</small>
            <p class="flow-muted">{{ t('evaluationFlow.sampleNotice') }}</p>
            <details v-if="item.limitations?.length" class="flow-usage"><summary>{{ t('evaluationFlow.usageNotes') }}</summary><ul class="flow-limitations"><li v-for="limit in item.limitations" :key="limit">{{ limit }}</li></ul></details>
            <button type="button" class="flow-button" :disabled="reading || !limits" @click="previewPublic(item)">{{ t('evaluationFlow.preview') }}<ArrowRight size="14px" /></button>
          </article>
        </div>
        <div v-if="mode === 'file' && !content" class="flow-stack">
          <div class="file-drop" :class="{ 'file-drop--over': dragging }" @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false" @drop.prevent="dropFile">
            <FileJson size="32px" aria-hidden="true" />
            <h3>{{ t('evaluationFlow.dropFile') }}</h3><p>{{ t('evaluationFlow.fileHint') }}</p>
            <label class="flow-button file-picker">{{ t('evaluationFlow.chooseFile') }}<input type="file" accept=".json,application/json" :disabled="reading || !limits" @change="pickFile" /></label>
          </div>
          <div class="flow-row"><button type="button" class="flow-link" @click="downloadTemplate"><Download size="14px" />{{ t('evaluationFlow.template') }}</button><small v-if="limits">{{ t('evaluationFlow.sizeLimit', { size: (limits.max_request_body_bytes / 1048576).toFixed(1) }) }}</small></div>
          <p class="flow-notice">{{ t('evaluationFlow.documentHint') }} <RouterLink to="/platform/knowledge-bases" @click="close(false)">{{ t('evaluationFlow.goKnowledge') }}</RouterLink></p>
        </div>
        <p v-if="reading" class="flow-notice" role="status"><LoaderCircle size="16px" class="spin" />{{ t('evaluationFlow.reading') }}</p>
        <div v-if="error" class="flow-error" role="alert">{{ error }}</div>
        <section v-if="content" class="flow-stack">
          <div class="flow-row"><h3>{{ t('evaluationFlow.preview') }}</h3><button v-if="!locked" class="flow-link" type="button" @click="resetDraft">{{ t('evaluationFlow.changeSource') }}</button></div>
          <div class="flow-counts"><div><strong>{{ content.passages.length }}</strong><span>{{ t('evaluationFlow.passages') }}</span></div><div><strong>{{ content.questions.length }}</strong><span>{{ t('evaluation.questions') }}</span></div><div><strong>{{ content.relevance.length }}</strong><span>{{ t('evaluationFlow.relevance') }}</span></div></div>
          <div v-if="selectedPublic" class="flow-notice"><span>{{ selectedPublic.license }}</span><a v-if="safeSource(selectedPublic.source_url)" :href="safeSource(selectedPublic.source_url)" target="_blank" rel="noopener noreferrer">{{ t('evaluationFlow.source') }}</a><p>{{ t('evaluationFlow.sampleNotice') }}</p><details v-if="selectedPublic.limitations?.length" class="flow-usage"><summary>{{ t('evaluationFlow.usageNotes') }}</summary><ul class="flow-limitations"><li v-for="limit in selectedPublic.limitations" :key="limit">{{ limit }}</li></ul></details></div>
          <details class="flow-card"><summary>{{ t('evaluationFlow.sample') }}</summary><div v-for="question in content.questions.slice(0, 3)" :key="question.qid" class="flow-sample"><h4>{{ question.question }}</h4><p>{{ question.answer || t('evaluationFlow.unanswerable') }}</p></div></details>
          <template v-if="!imported">
            <label class="flow-field"><span>{{ t('evaluationFlow.importTarget') }}</span><select v-model="targetId" :disabled="locked"><option value="">{{ t('evaluationFlow.newDataset') }}</option><option v-for="dataset in tenantDatasets" :key="dataset.id" :value="dataset.id">{{ dataset.name }} · {{ t('evaluationFlow.addVersion') }}</option></select></label>
            <label v-if="!targetId" class="flow-field"><span>{{ t('evaluationFlow.name') }}</span><input v-model="name" :disabled="locked" :maxlength="limits?.max_name_chars" autocomplete="off" /></label>
            <label v-if="!targetId" class="flow-field"><span>{{ t('evaluationFlow.description') }}</span><textarea v-model="description" :disabled="locked" rows="2" /></label>
            <p class="flow-notice"><ShieldCheck size="18px" aria-hidden="true" />{{ t(targetId ? 'evaluationFlow.versionHint' : 'evaluationFlow.noModelCost') }}</p>
            <p v-if="attempted && error" class="flow-muted">{{ t(targetId ? 'evaluationFlow.versionRetryHint' : 'evaluationFlow.retryHint') }}</p>
            <div class="flow-actions">
              <button v-if="canManage" type="button" class="flow-button flow-button--primary" :disabled="submitting || !limits || (!targetId && !name.trim()) || (Boolean(targetId) && attempted)" @click="submitImport"><Upload size="15px" />{{ t(submitting ? 'evaluationFlow.importing' : attempted ? 'evaluationFlow.retryImport' : 'evaluationFlow.confirmImport') }}</button>
              <button v-if="attempted && !submitting" type="button" class="flow-link" @click="resetDraft">{{ t('evaluationFlow.startOver') }}</button>
            </div>
          </template>
          <div v-else class="flow-success" role="status"><CheckCircle2 size="20px" /><h3>{{ t('evaluationFlow.imported') }}</h3><p>{{ imported.dataset.name }} · v{{ imported.version.version_number }}</p><button type="button" class="flow-button flow-button--primary" @click="emit('create', imported.dataset.id, imported.version.id)">{{ t('evaluationFlow.newRun') }}<ArrowRight size="15px" /></button><button type="button" class="flow-link" @click="resetDraft">{{ t('evaluationFlow.importAnother') }}</button></div>
        </section>
      </template>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { ArrowRightIcon as ArrowRight, CheckCircleIcon as CheckCircle2, ServerIcon as Database, DownloadIcon as Download, LinkIcon as ExternalLink, FileCodeIcon as FileJson, LoadingIcon as LoaderCircle, AddIcon as Plus, SecuredIcon as ShieldCheck, UploadIcon as Upload, CloseIcon as X } from 'tdesign-icons-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { createDatasetVersion, getPublicDataset, getPublicDatasetCatalog, importEvaluationDataset, listDatasetVersions, listEvaluationDatasets, type DatasetContent, type DatasetImportResult, type DatasetLimits, type DatasetVersion, type EvaluationDataset, type PublicDataset } from '@/api/evaluation/datasets'
import { createEvaluationRequestGate } from '@/api/evaluation/requestGate'
import { createDatasetImportSession } from './datasetImportSession'
import { DATASET_TEMPLATE, DatasetValidationError, readDatasetFile, validateDatasetContent, validateImportEnvelope, utf8Size } from './datasetValidation'
import { evaluationErrorMessage } from './evaluationErrors'

const props = defineProps<{ visible: boolean; tenantKey: string; canManage: boolean }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; imported: [result: DatasetImportResult]; create: [datasetId: string, versionId: string] }>()
const { t } = useI18n()
const mode = ref<'library' | 'catalog' | 'file'>('catalog')
const tabs = computed(() => [{ id: 'catalog' as const, label: t('evaluationFlow.publicCatalog') }, { id: 'file' as const, label: t('evaluationFlow.localFile') }, { id: 'library' as const, label: t('evaluationFlow.myDatasets') }])
const datasets = ref<EvaluationDataset[]>([]), catalog = ref<PublicDataset[]>([]), limits = ref<DatasetLimits>()
const tenantDatasets = computed(() => datasets.value.filter(item => item.scope === 'tenant'))
const loading = ref(false), loadError = ref(''), error = ref(''), reading = ref(false), dragging = ref(false)
const content = ref<DatasetContent>(), selectedPublic = ref<PublicDataset>(), name = ref(''), description = ref(''), targetId = ref('')
const submitting = ref(false), attempted = ref(false), imported = ref<DatasetImportResult>()
const locked = computed(() => submitting.value || attempted.value)
const inspectedId = ref(''), versions = ref<DatasetVersion[]>([]), versionsLoading = ref(false), versionsError = ref('')
const resourcesGate = createEvaluationRequestGate(), readGate = createEvaluationRequestGate(), versionGate = createEvaluationRequestGate(), writeGate = createEvaluationRequestGate()
const session = createDatasetImportSession(async request => {
  if (!limits.value) throw new Error(t('evaluationFlow.limitsUnavailable'))
  validateImportEnvelope(request, limits.value)
  return importEvaluationDataset(request)
})
function message(reason: unknown): string {
  if (reason instanceof DatasetValidationError) return t(`evaluationFlow.errors.${reason.code}`, { path: reason.path, limit: reason.limit ?? 0 })
  return evaluationErrorMessage(reason, t('evaluation.unknownError'))
}
function safeSource(url: string): string | undefined { try { const u = new URL(url); return u.protocol === 'https:' ? u.href : undefined } catch { return undefined } }
async function loadResources() {
  const token = resourcesGate.begin(props.tenantKey)
  loading.value = true; loadError.value = ''
  const [library, publicData] = await Promise.allSettled([listEvaluationDatasets(), getPublicDatasetCatalog()])
  if (!resourcesGate.isCurrent(token)) return
  if (library.status === 'fulfilled') datasets.value = library.value
  if (publicData.status === 'fulfilled') { catalog.value = publicData.value.items; limits.value = publicData.value.limits }
  loadError.value = [library, publicData].filter(item => item.status === 'rejected').map(item => message((item as PromiseRejectedResult).reason)).join(' · ')
  loading.value = false
}
function resetDraft() {
  readGate.invalidate(); writeGate.invalidate(); session.reset()
  content.value = undefined; selectedPublic.value = undefined; imported.value = undefined; attempted.value = false
  name.value = ''; description.value = ''; targetId.value = ''; error.value = ''; reading.value = false; submitting.value = false
}
async function previewPublic(item: PublicDataset) {
  if (!limits.value || locked.value) return
  const token = readGate.begin(item.id); reading.value = true; error.value = ''
  try {
    const pkg = await getPublicDataset(item.id)
    if (!readGate.isCurrent(token)) return
    content.value = validateDatasetContent(pkg.content, limits.value)
    name.value = pkg.name; description.value = pkg.description; selectedPublic.value = item
  } catch (reason) { if (readGate.isCurrent(token)) error.value = message(reason) }
  finally { if (readGate.isCurrent(token)) reading.value = false }
}
async function previewFile(file?: File) {
  if (!file || !limits.value || locked.value) return
  const token = readGate.begin(file.name); reading.value = true; error.value = ''
  try {
    const parsed = await readDatasetFile(file, limits.value)
    if (!readGate.isCurrent(token)) return
    content.value = parsed; name.value = file.name.replace(/\.json$/i, ''); selectedPublic.value = undefined
  } catch (reason) { if (readGate.isCurrent(token)) error.value = message(reason) }
  finally { if (readGate.isCurrent(token)) reading.value = false }
}
function pickFile(event: Event) { const input = event.target as HTMLInputElement; void previewFile(input.files?.[0]); input.value = '' }
function dropFile(event: DragEvent) { dragging.value = false; if (event.dataTransfer?.files.length !== 1) { error.value = t('evaluationFlow.oneFile'); return }; void previewFile(event.dataTransfer.files[0]) }
function downloadTemplate() {
  const url = URL.createObjectURL(new Blob([JSON.stringify(DATASET_TEMPLATE, null, 2)], { type: 'application/json' }))
  const a = document.createElement('a'); a.href = url; a.download = 'evaluation-dataset-template.json'; a.click(); URL.revokeObjectURL(url)
}
async function inspectVersions(dataset: EvaluationDataset) {
  const token = versionGate.begin(dataset.id); inspectedId.value = dataset.id; versions.value = []; versionsLoading.value = true; versionsError.value = ''
  try { const items = await listDatasetVersions(dataset.id); if (versionGate.isCurrent(token)) versions.value = items }
  catch (reason) { if (versionGate.isCurrent(token)) versionsError.value = message(reason) }
  finally { if (versionGate.isCurrent(token)) versionsLoading.value = false }
}
function addVersion(dataset: EvaluationDataset) { resetDraft(); mode.value = 'file'; targetId.value = dataset.id }
async function submitImport() {
  if (!content.value || !limits.value || submitting.value || imported.value || !props.canManage || (targetId.value && attempted.value)) return
  if (targetId.value && !tenantDatasets.value.some(item => item.id === targetId.value)) return
  const token = writeGate.begin(props.tenantKey); submitting.value = true; error.value = ''
  try {
    let result: DatasetImportResult | undefined
    if (targetId.value) {
      if (utf8Size(JSON.stringify(content.value)) > limits.value.max_request_body_bytes) throw new DatasetValidationError('fileSize', '', limits.value.max_request_body_bytes)
      attempted.value = true
      const dataset = tenantDatasets.value.find(item => item.id === targetId.value)!
      const version = await createDatasetVersion(dataset.id, content.value)
      result = { dataset, version, replayed: false }
    } else {
      validateImportEnvelope({ name: name.value.trim(), description: description.value, content: content.value, request_id: '00000000-0000-4000-8000-000000000000' }, limits.value)
      attempted.value = true
      result = await session.submit({ name: name.value.trim(), description: description.value, content: content.value })
    }
    if (!writeGate.isCurrent(token) || !result) return
    imported.value = result; emit('imported', result); void loadResources()
  } catch (reason) { if (writeGate.isCurrent(token)) error.value = message(reason) }
  finally { if (writeGate.isCurrent(token)) submitting.value = false }
}
function close(value: boolean) { if (!submitting.value) emit('update:visible', value) }
watch(() => props.visible, value => { if (value) void loadResources(); else { readGate.invalidate(); reading.value = false } }, { immediate: true })
watch(() => props.tenantKey, () => { resourcesGate.invalidate(); versionGate.invalidate(); resetDraft(); datasets.value = []; catalog.value = []; limits.value = undefined; inspectedId.value = ''; versions.value = []; if (props.visible) void loadResources() }, { flush: 'sync' })
onBeforeUnmount(() => { resourcesGate.invalidate(); versionGate.invalidate(); resetDraft() })
</script>

<style lang="less" src="./evaluationFlow.less"></style>
