<template>
  <SettingDrawer :visible="visible" :title="t('evaluationFlow.newRun')" :description="t('evaluationFlow.runHint')" width="600px" :min-width="320" :resizable="false" :close-on-esc-keydown="!submitting" hide-footer @update:visible="close">
    <template #headerIcon><FlaskConical size="18px" aria-hidden="true" /></template>
    <template #header-actions><t-button theme="default" variant="text" shape="square" :aria-label="t('common.close')" :disabled="submitting" @click="close(false)"><X size="18px" aria-hidden="true" /></t-button></template>
    <form class="evaluation-flow flow-stack" @submit.prevent="submit">
      <p v-if="loading" class="flow-notice" role="status"><LoaderCircle size="16px" class="spin" />{{ t('evaluation.loading') }}</p>
      <div v-if="loadError" class="flow-error" role="alert">{{ loadError }}<button type="button" class="flow-link" @click="loadResources">{{ t('evaluation.refresh') }}</button></div>
      <label class="flow-field"><span>{{ t('evaluation.dataset') }}</span><select v-model="draft.datasetId" :disabled="submitting || loading" required @change="selectDataset()"><option value="" disabled>{{ t('evaluationFlow.chooseDataset') }}</option><option v-for="dataset in datasets" :key="dataset.id" :value="dataset.id">{{ dataset.name }}</option></select></label>
      <p v-if="!loading && !datasets.length && !loadError" class="flow-notice">{{ t('evaluationFlow.noDatasets') }}<button type="button" class="flow-link" @click="emit('import')">{{ t('evaluationFlow.openImport') }}</button></p>
      <label class="flow-field"><span>{{ t('evaluation.datasetVersion') }}</span><select v-model="draft.versionId" :disabled="submitting || versionsLoading || !draft.datasetId" required><option value="" disabled>{{ t(versionsLoading ? 'evaluation.loading' : 'evaluationFlow.chooseVersion') }}</option><option v-for="version in versions" :key="version.id" :value="version.id">v{{ version.version_number }} · {{ t('evaluationFlow.questionCount', { count: version.question_count }) }}</option></select><small>{{ t('evaluationFlow.frozenVersion') }}</small></label>
      <p v-if="versionError" class="flow-error" role="alert">{{ versionError }}<button type="button" class="flow-link" @click="selectDataset()">{{ t('evaluation.refresh') }}</button></p>
      <label class="flow-field"><span>{{ t('evaluationFlow.knowledgeBase') }}</span><select v-model="draft.knowledgeBaseId" :disabled="submitting || loading" required @change="loadKnowledgeBase"><option value="" disabled>{{ t('evaluationFlow.chooseKnowledgeBase') }}</option><option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :disabled="!kb.embedding_model_id">{{ kb.name }}</option></select><small>{{ t('evaluationFlow.sourceConfigHint') }}</small></label>
      <p v-if="!loading && !knowledgeBases.length && !loadError" class="flow-notice">{{ t('evaluationFlow.noKnowledgeBases') }}<RouterLink to="/platform/knowledge-bases" @click="close(false)">{{ t('evaluationFlow.goKnowledge') }}</RouterLink></p>
      <p v-if="kbLoading" role="status" class="flow-muted">{{ t('evaluation.loading') }}</p>
      <p v-if="kbError" class="flow-error" role="alert">{{ kbError }}<button type="button" class="flow-link" @click="loadKnowledgeBase">{{ t('evaluation.refresh') }}</button></p>
      <dl v-if="selectedKB" class="flow-facts"><div><dt>{{ t('evaluationFlow.embedding') }}</dt><dd>{{ modelName(selectedKB.embedding_model_id) }}</dd></div><div><dt>{{ t('evaluationFlow.chunking') }}</dt><dd>{{ t('evaluationFlow.datasetChunking') }}</dd></div></dl>
      <label class="flow-field"><span>{{ t('evaluationFlow.chatModel') }}</span><select v-model="draft.chatId" :disabled="submitting || loading" required><option value="" disabled>{{ t('evaluationFlow.chooseModel') }}</option><option v-for="model in chatModels" :key="model.id" :value="model.id">{{ model.display_name || model.name }}</option></select></label>
      <p v-if="!loading && !chatModels.length && !loadError" class="flow-notice">{{ t('evaluationFlow.noChatModels') }}</p>
      <label class="flow-field"><span>{{ t('evaluationFlow.rerankModel') }}</span><select v-model="draft.rerankId" :disabled="submitting || loading"><option value="">{{ t('evaluationFlow.autoRerank') }}</option><option v-for="model in rerankModels" :key="model.id" :value="model.id">{{ model.display_name || model.name }}</option></select><small>{{ t('evaluationFlow.autoRerankHint') }}</small></label>
      <details class="flow-card"><summary>{{ t('evaluationFlow.optionalSettings') }}</summary><div class="flow-stack flow-advanced"><label class="flow-field"><span>{{ t('evaluationFlow.topK') }}</span><input v-model="draft.topK" type="number" min="1" max="100" step="1" :disabled="submitting" :placeholder="t('evaluationFlow.serviceDefault')" /></label><label class="flow-field"><span>{{ t('evaluationFlow.seed') }}</span><input v-model="draft.seed" type="number" step="1" :disabled="submitting" :placeholder="t('evaluationFlow.seedDefault')" /><small>{{ t('evaluationFlow.seedHint') }}</small></label></div></details>
      <div class="flow-cost"><Coins size="20px" aria-hidden="true" /><div><h3>{{ t('evaluationFlow.modelCalls') }}</h3><p>{{ t('evaluationFlow.costNotice') }}</p><small v-if="selectedVersion">{{ t('evaluationFlow.runScale', { passages: selectedVersion.passage_count, questions: selectedVersion.question_count }) }}</small></div></div>
      <label class="flow-confirm"><input v-model="confirmed" type="checkbox" :disabled="submitting" /><span>{{ t('evaluationFlow.costConfirm') }}</span></label>
      <p v-if="error" class="flow-error" role="alert">{{ error }}</p>
      <p v-if="uncertain" class="flow-notice">{{ t('evaluationFlow.runUncertain') }}<button type="button" class="flow-link" @click="emit('refresh'); close(false)">{{ t('evaluationFlow.checkRuns') }}</button></p>
      <button type="submit" class="flow-button flow-button--primary" :disabled="!canSubmit"><Play size="15px" aria-hidden="true" />{{ t(submitting ? 'evaluationFlow.creating' : 'evaluationFlow.confirmRun') }}</button>
    </form>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { MoneyIcon as Coins, ChartScatterIcon as FlaskConical, LoadingIcon as LoaderCircle, PlayIcon as Play, CloseIcon as X } from 'tdesign-icons-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { getKnowledgeBaseById, listKnowledgeBases, type KnowledgeBaseConfigurationView } from '@/api/knowledge-base'
import { listModels, type ModelConfig } from '@/api/model'
import { createEvaluation, listDatasetVersions, listEvaluationDatasets, type DatasetVersion, type EvaluationDataset } from '@/api/evaluation/datasets'
import type { EvaluationTask } from '@/api/evaluation'
import { createEvaluationRequestGate } from '@/api/evaluation/requestGate'
import { buildEvaluationCreation } from './evaluationCreation'
import { evaluationErrorMessage } from './evaluationErrors'

const props = defineProps<{ visible: boolean; tenantKey: string; datasetId?: string; versionId?: string }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; created: [task: EvaluationTask]; import: []; refresh: [] }>()
const { t } = useI18n()
const draft = reactive({ datasetId: '', versionId: '', knowledgeBaseId: '', chatId: '', rerankId: '', topK: '', seed: '' })
const datasets = ref<EvaluationDataset[]>([]), versions = ref<DatasetVersion[]>([]), models = ref<ModelConfig[]>([]), knowledgeBases = ref<KnowledgeBaseConfigurationView[]>([])
const selectedKB = ref<KnowledgeBaseConfigurationView>(), selectedVersion = computed(() => versions.value.find(v => v.id === draft.versionId))
const chatModels = computed(() => models.value.filter(m => m.id && m.type === 'KnowledgeQA' && !m.deleted_at))
const rerankModels = computed(() => models.value.filter(m => m.id && m.type === 'Rerank' && !m.deleted_at))
const loading = ref(false), versionsLoading = ref(false), kbLoading = ref(false), submitting = ref(false), confirmed = ref(false), uncertain = ref(false)
const loadError = ref(''), versionError = ref(''), kbError = ref(''), error = ref('')
const resourcesGate = createEvaluationRequestGate(), versionGate = createEvaluationRequestGate(), kbGate = createEvaluationRequestGate(), createGate = createEvaluationRequestGate()
const canSubmit = computed(() => confirmed.value && !submitting.value && !loading.value && !versionsLoading.value && !kbLoading.value && !loadError.value && !versionError.value && !kbError.value && !uncertain.value && !!selectedVersion.value && !!selectedKB.value?.embedding_model_id && chatModels.value.some(m => m.id === draft.chatId))
const message = (reason: unknown) => evaluationErrorMessage(reason, t('evaluation.unknownError'))
function modelName(id: string) { const model = models.value.find(m => m.id === id); return model?.display_name || model?.name || id }
function unwrapKB(response: unknown): KnowledgeBaseConfigurationView {
  const r = response as { success?: boolean; data?: KnowledgeBaseConfigurationView }
  if (!r?.success || !r.data?.id) throw new Error(t('evaluationFlow.resourcesIncomplete'))
  return r.data
}
async function loadResources() {
  const token = resourcesGate.begin(props.tenantKey); loading.value = true; loadError.value = ''
  const results = await Promise.allSettled([listEvaluationDatasets(), listModels(), listKnowledgeBases()])
  if (!resourcesGate.isCurrent(token)) return
  const [d, m, k] = results
  if (d.status === 'fulfilled') datasets.value = d.value
  if (m.status === 'fulfilled') models.value = m.value
  if (k.status === 'fulfilled') { const r = k.value as { success?: boolean; data?: KnowledgeBaseConfigurationView[] }; if (r.success && Array.isArray(r.data)) knowledgeBases.value = r.data; else loadError.value = t('evaluationFlow.resourcesIncomplete') }
  const failures = results.filter(result => result.status === 'rejected') as PromiseRejectedResult[]
  if (failures.length) loadError.value = failures.map(r => message(r.reason)).join(' · ')
  loading.value = false
  if (!draft.datasetId && props.datasetId && datasets.value.some(d => d.id === props.datasetId)) { draft.datasetId = props.datasetId; void selectDataset(props.versionId) }
  if (!draft.chatId) draft.chatId = chatModels.value.find(m => m.is_default)?.id || ''
}
async function selectDataset(preferredVersion?: string) {
  const id = draft.datasetId, token = versionGate.begin(id)
  draft.versionId = ''; versions.value = []; versionError.value = ''; versionsLoading.value = true
  if (!id) { versionsLoading.value = false; return }
  try {
    const items = await listDatasetVersions(id)
    if (!versionGate.isCurrent(token)) return
    versions.value = items
    const preferred = preferredVersion || datasets.value.find(d => d.id === id)?.current_version_id
    draft.versionId = items.find(v => v.id === preferred)?.id || items[0]?.id || ''
  } catch (reason) { if (versionGate.isCurrent(token)) versionError.value = message(reason) }
  finally { if (versionGate.isCurrent(token)) versionsLoading.value = false }
}
async function loadKnowledgeBase() {
  const id = draft.knowledgeBaseId, token = kbGate.begin(id); selectedKB.value = undefined; kbLoading.value = true; kbError.value = ''
  try { const response = await getKnowledgeBaseById(id); if (kbGate.isCurrent(token)) selectedKB.value = unwrapKB(response) }
  catch (reason) { if (kbGate.isCurrent(token)) kbError.value = message(reason) }
  finally { if (kbGate.isCurrent(token)) kbLoading.value = false }
}
async function submit() {
  if (!canSubmit.value) return
  let request
  try { request = buildEvaluationCreation(draft) } catch (reason) { error.value = t(`evaluationFlow.errors.${message(reason)}`); return }
  const token = createGate.begin(props.tenantKey); submitting.value = true; error.value = ''
  try {
    const task = await createEvaluation(request)
    if (!createGate.isCurrent(token)) return
    emit('created', task); emit('update:visible', false)
  } catch (reason) {
    if (!createGate.isCurrent(token)) return
    error.value = message(reason)
    const status = (reason as { response?: { status?: number }; status?: number })?.response?.status ?? (reason as { status?: number })?.status
    uncertain.value = !status || status >= 500
    confirmed.value = false
  } finally { if (createGate.isCurrent(token)) submitting.value = false }
}
function close(value: boolean) { if (!submitting.value) emit('update:visible', value) }
function reset() {
  resourcesGate.invalidate(); versionGate.invalidate(); kbGate.invalidate(); createGate.invalidate()
  Object.assign(draft, { datasetId: '', versionId: '', knowledgeBaseId: '', chatId: '', rerankId: '', topK: '', seed: '' })
  versions.value = []; selectedKB.value = undefined; confirmed.value = false; uncertain.value = false; submitting.value = false
  error.value = ''; versionError.value = ''; kbError.value = ''; kbLoading.value = false; versionsLoading.value = false
}
watch(() => props.visible, value => { if (value) { reset(); void loadResources() } else { resourcesGate.invalidate(); versionGate.invalidate(); kbGate.invalidate() } }, { immediate: true })
watch(() => props.tenantKey, () => { reset(); datasets.value = []; knowledgeBases.value = []; models.value = []; if (props.visible) void loadResources() }, { flush: 'sync' })
watch(draft, () => { confirmed.value = false })
onBeforeUnmount(reset)
</script>

<style lang="less" src="./evaluationFlow.less"></style>
