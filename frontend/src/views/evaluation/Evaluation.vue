<template>
  <div class="evaluation-page">
    <div class="page-heading">
      <div>
        <h1>RAG 评估</h1>
        <p>用固定数据集评估、比较知识库问答配置，并保留每次运行的检索与回答证据。</p>
      </div>
      <t-button v-if="canEdit && activeTab === 'runs'" theme="primary" @click="openRunDialog">创建评估运行</t-button>
      <t-button v-else-if="canEdit && activeTab === 'datasets'" theme="primary" @click="openDatasetDialog()">创建数据集</t-button>
    </div>

    <t-tabs v-model="activeTab" class="evaluation-tabs">
      <t-tab-panel value="datasets" label="评估数据集">
        <div class="dataset-layout">
          <section class="panel dataset-panel">
            <t-table row-key="id" :data="datasets" :columns="datasetColumns" :loading="loadingDatasets" hover @row-click="selectDataset">
              <template #name="{ row }"><strong>{{ row.name }}</strong><div class="muted">{{ row.description || '无描述' }}</div></template>
              <template #operation="{ row }">
                <t-space v-if="canEdit">
                  <t-link theme="primary" @click.stop="openDatasetDialog(row)">编辑</t-link>
                  <t-popconfirm content="删除数据集后不可继续编辑，历史运行不受影响。" @confirm="removeDataset(row)"><t-link theme="danger" @click.stop>删除</t-link></t-popconfirm>
                </t-space>
              </template>
            </t-table>
          </section>
          <section class="panel sample-panel">
            <div class="panel-heading">
              <div><h2>{{ selectedDataset?.name || '选择一个数据集' }}</h2><span class="muted">问题、参考答案和可选参考上下文</span></div>
              <t-button v-if="canEdit" size="small" :disabled="!selectedDataset" @click="openSampleDialog()">添加样本</t-button>
            </div>
            <t-table row-key="id" :data="samples" :columns="sampleColumns" :loading="loadingSamples" table-layout="fixed">
              <template #question="{ row }"><div class="cell-primary">{{ row.question }}</div><div class="muted clamp">{{ row.reference_answer }}</div></template>
              <template #contexts="{ row }">{{ row.reference_contexts?.length || 0 }}</template>
              <template #operation="{ row }">
                <t-space v-if="canEdit"><t-link theme="primary" @click="openSampleDialog(row)">编辑</t-link><t-popconfirm content="确认删除该样本？" @confirm="removeSample(row)"><t-link theme="danger">删除</t-link></t-popconfirm></t-space>
              </template>
            </t-table>
            <t-empty v-if="!selectedDataset" description="从左侧选择数据集后管理样本" />
          </section>
        </div>
      </t-tab-panel>

      <t-tab-panel value="runs" label="评估运行">
        <section class="panel">
          <t-table row-key="id" :data="runs" :columns="runColumns" :loading="loadingRuns" hover @row-click="openRunDetail">
            <template #status="{ row }"><t-tag :theme="statusTheme(row.status)" variant="light">{{ statusText(row.status) }}</t-tag></template>
            <template #progress="{ row }"><t-progress size="small" :percentage="progress(row)" :label="`${row.finished_samples}/${row.total_samples}`" /></template>
            <template #metrics="{ row }"><span>{{ Object.keys(row.aggregate_metric_scores || {}).length }} 项</span></template>
            <template #operation="{ row }"><t-link theme="primary" @click.stop="openRunDetail({ row })">查看详情</t-link></template>
          </t-table>
        </section>
      </t-tab-panel>

      <t-tab-panel value="compare" label="运行对比">
        <section class="panel compare-panel">
          <div class="compare-controls">
            <t-select v-model="baselineRunId" placeholder="选择基线运行" filterable><t-option v-for="run in completedRuns" :key="run.id" :value="run.id" :label="runLabel(run)" /></t-select>
            <span class="compare-arrow">→</span>
            <t-select v-model="candidateRunId" placeholder="选择候选运行" filterable><t-option v-for="run in completedRuns" :key="run.id" :value="run.id" :label="runLabel(run)" /></t-select>
            <t-button :loading="comparing" @click="compareRuns">开始对比</t-button>
          </div>
          <t-table v-if="comparison" row-key="name" :data="comparison.metrics" :columns="comparisonColumns">
            <template #metric="{ row }"><strong>{{ row.name }}</strong><span class="metric-version">{{ row.version }}</span></template>
            <template #score="{ row }">{{ formatScore(row.baseline_score) }} → {{ formatScore(row.candidate_score) }}</template>
            <template #delta="{ row }"><span :class="row.improved ? 'improved' : row.delta < 0 ? 'declined' : ''">{{ signedScore(row.delta) }}</span></template>
            <template #improved="{ row }"><t-tag :theme="row.improved ? 'success' : 'default'" variant="light">{{ row.improved ? '改善' : '未改善' }}</t-tag></template>
          </t-table>
          <t-empty v-else description="选择同一数据集的两个已完成运行进行对比" />
        </section>
      </t-tab-panel>
    </t-tabs>

    <t-dialog v-model:visible="datasetDialogVisible" :header="editingDataset ? '编辑数据集' : '创建数据集'" :confirm-btn="{ content: '保存', loading: saving }" @confirm="saveDataset">
      <t-form label-align="top"><t-form-item label="名称" required><t-input v-model="datasetForm.name" maxlength="255" /></t-form-item><t-form-item label="描述"><t-textarea v-model="datasetForm.description" :autosize="{ minRows: 3, maxRows: 6 }" /></t-form-item></t-form>
    </t-dialog>

    <t-dialog v-model:visible="sampleDialogVisible" width="720px" :header="editingSample ? '编辑评估样本' : '添加评估样本'" :confirm-btn="{ content: '保存', loading: saving }" @confirm="saveSample">
      <t-form label-align="top"><t-form-item label="问题" required><t-textarea v-model="sampleForm.question" :autosize="{ minRows: 2, maxRows: 5 }" /></t-form-item><t-form-item label="参考答案" required><t-textarea v-model="sampleForm.reference_answer" :autosize="{ minRows: 3, maxRows: 8 }" /></t-form-item><t-form-item label="参考上下文（JSON 数组）"><t-textarea v-model="sampleContextsText" :autosize="{ minRows: 5, maxRows: 12 }" placeholder='[{"text":"...","knowledge_id":"可选","chunk_id":"可选"}]' /><div class="field-hint">检索指标优先按 chunk_id 匹配，缺失时按规范化文本精确匹配。</div></t-form-item></t-form>
    </t-dialog>

    <t-dialog v-model:visible="runDialogVisible" width="760px" header="创建评估运行" :confirm-btn="{ content: '开始运行', loading: saving }" @confirm="saveRun">
      <t-form label-align="top" class="run-form">
        <div class="form-grid"><t-form-item label="数据集" required><t-select v-model="runForm.dataset_id" filterable><t-option v-for="d in datasets" :key="d.id" :value="d.id" :label="`${d.name}（${d.sample_count}）`" /></t-select></t-form-item><t-form-item label="知识库" required><t-select v-model="runForm.knowledge_base_id" filterable><t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" /></t-select></t-form-item><t-form-item label="问答模型" required><t-select v-model="runForm.chat_model_id" filterable><t-option v-for="m in chatModels" :key="m.id" :value="m.id" :label="m.display_name || m.name" /></t-select></t-form-item><t-form-item label="重排模型"><t-select v-model="runForm.rerank_model_id" clearable filterable><t-option v-for="m in rerankModels" :key="m.id" :value="m.id" :label="m.display_name || m.name" /></t-select></t-form-item></div>
        <div class="form-grid config-grid"><t-form-item label="向量阈值"><t-input-number v-model="runForm.vector_threshold" :min="0" :max="1" :step="0.05" /></t-form-item><t-form-item label="关键词阈值"><t-input-number v-model="runForm.keyword_threshold" :min="0" :max="1" :step="0.05" /></t-form-item><t-form-item label="召回 Top K"><t-input-number v-model="runForm.embedding_top_k" :min="1" /></t-form-item><t-form-item label="重排 Top K"><t-input-number v-model="runForm.rerank_top_k" :min="1" /></t-form-item><t-form-item label="重排阈值"><t-input-number v-model="runForm.rerank_threshold" :step="0.05" /></t-form-item></div>
        <t-form-item label="评估指标" required><t-checkbox-group v-model="runForm.metric_names" :options="metricOptions" /></t-form-item>
      </t-form>
    </t-dialog>

    <t-drawer v-model:visible="runDrawerVisible" size="82%" :header="selectedRun ? `运行详情 · ${selectedRun.dataset_name}` : '运行详情'">
      <template v-if="selectedRun">
        <div class="run-summary"><div><span>状态</span><t-tag :theme="statusTheme(selectedRun.status)" variant="light">{{ statusText(selectedRun.status) }}</t-tag></div><div><span>进度</span><strong>{{ selectedRun.finished_samples }}/{{ selectedRun.total_samples }}</strong></div><div><span>失败样本</span><strong>{{ selectedRun.failed_samples }}</strong></div><div><span>运行 ID</span><code>{{ selectedRun.id }}</code></div></div>
        <t-alert v-if="selectedRun.error" theme="error" :message="selectedRun.error" />
        <details class="snapshot"><summary>配置快照</summary><pre>{{ JSON.stringify(selectedRun.config_snapshot, null, 2) }}</pre></details>
        <h3>聚合指标</h3><div class="metric-grid"><div v-for="score in metricScoreList(selectedRun.aggregate_metric_scores)" :key="score.name" class="metric-card"><div>{{ score.name }} <small>{{ score.version }}</small></div><strong>{{ score.score == null ? '—' : formatScore(score.score) }}</strong><span>{{ score.scored_sample_count || 0 }}/{{ score.total_sample_count || selectedRun.total_samples }} 个样本</span></div></div>
        <h3>样本结果</h3><t-table row-key="id" :data="runResults" :columns="resultColumns" :loading="loadingRunResults" table-layout="fixed" @row-click="selectResult"><template #status="{ row }"><t-tag :theme="row.status === 'completed' ? 'success' : row.status === 'failed' ? 'danger' : 'default'" variant="light">{{ row.status }}</t-tag></template><template #answer="{ row }"><div class="clamp">{{ row.generated_answer || row.error || '—' }}</div></template><template #metrics="{ row }">{{ Object.values(row.metric_scores || {}).filter((m: any) => m.status === 'scored').length }}</template></t-table>
        <div v-if="selectedResult" class="result-detail"><h3>样本证据</h3><div class="evidence-grid"><div><h4>问题</h4><p>{{ selectedResult.question }}</p><h4>参考答案</h4><p>{{ selectedResult.reference_answer }}</p><h4>生成答案</h4><p>{{ selectedResult.generated_answer || selectedResult.error }}</p></div><div><h4>检索上下文</h4><ol><li v-for="context in selectedResult.retrieved_contexts" :key="`${context.rank}-${context.chunk_id}`"><span class="muted">#{{ context.rank }} · {{ formatScore(context.score) }}</span><p>{{ context.text }}</p></li></ol></div></div></div>
      </template>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import { listKnowledgeBases } from '@/api/knowledge-base'
import { listModels, type ModelConfig } from '@/api/model'
import {
  compareEvaluationRuns, createEvaluationDataset, createEvaluationRun, createEvaluationSample,
  deleteEvaluationDataset, deleteEvaluationSample, getEvaluationRun, listEvaluationDatasets,
  listEvaluationMetrics, listEvaluationRunResults, listEvaluationRuns, listEvaluationSamples,
  updateEvaluationDataset, updateEvaluationSample,
  type EvaluationDataset, type EvaluationRun, type EvaluationRunResult, type EvaluationSample,
  type MetricScore, type ReferenceContext, type RunComparison,
} from '@/api/evaluation'

const activeTab = ref('datasets')
const authStore = useAuthStore()
const canEdit = computed(() => authStore.hasRole('admin'))
const datasets = ref<EvaluationDataset[]>([])
const samples = ref<EvaluationSample[]>([])
const runs = ref<EvaluationRun[]>([])
const metrics = ref<Array<{ name: string; version: string; category: string }>>([])
const knowledgeBases = ref<any[]>([])
const models = ref<ModelConfig[]>([])
const selectedDataset = ref<EvaluationDataset | null>(null)
const selectedRun = ref<EvaluationRun | null>(null)
const selectedResult = ref<EvaluationRunResult | null>(null)
const runResults = ref<EvaluationRunResult[]>([])
const comparison = ref<RunComparison | null>(null)
const baselineRunId = ref('')
const candidateRunId = ref('')
const loadingDatasets = ref(false)
const loadingSamples = ref(false)
const loadingRuns = ref(false)
const loadingRunResults = ref(false)
const saving = ref(false)
const comparing = ref(false)
const datasetDialogVisible = ref(false)
const sampleDialogVisible = ref(false)
const runDialogVisible = ref(false)
const runDrawerVisible = ref(false)
const editingDataset = ref<EvaluationDataset | null>(null)
const editingSample = ref<EvaluationSample | null>(null)
const sampleContextsText = ref('[]')
const datasetForm = reactive({ name: '', description: '' })
const sampleForm = reactive({ question: '', reference_answer: '' })
const runForm = reactive({ dataset_id: '', knowledge_base_id: '', chat_model_id: '', rerank_model_id: '', vector_threshold: 0.15, keyword_threshold: 0.3, embedding_top_k: 50, rerank_top_k: 10, rerank_threshold: 0.2, metric_names: [] as string[] })

const datasetColumns = [{ colKey: 'name', title: '数据集', cell: 'name' }, { colKey: 'sample_count', title: '样本数', width: 90 }, { colKey: 'operation', title: '操作', width: 110, cell: 'operation' }]
const sampleColumns = [{ colKey: 'question', title: '问题与参考答案', cell: 'question', ellipsis: true }, { colKey: 'contexts', title: '上下文', width: 80, cell: 'contexts' }, { colKey: 'operation', title: '操作', width: 110, cell: 'operation' }]
const runColumns = [{ colKey: 'dataset_name', title: '数据集' }, { colKey: 'status', title: '状态', width: 100, cell: 'status' }, { colKey: 'progress', title: '进度', width: 220, cell: 'progress' }, { colKey: 'metrics', title: '聚合指标', width: 100, cell: 'metrics' }, { colKey: 'created_at', title: '创建时间', width: 190 }, { colKey: 'operation', title: '操作', width: 90, cell: 'operation' }]
const resultColumns = [{ colKey: 'sample_index', title: '#', width: 60 }, { colKey: 'question', title: '问题', ellipsis: true }, { colKey: 'answer', title: '生成答案 / 错误', cell: 'answer', ellipsis: true }, { colKey: 'status', title: '状态', width: 100, cell: 'status' }, { colKey: 'metrics', title: '已评分', width: 80, cell: 'metrics' }, { colKey: 'duration_ms', title: '耗时(ms)', width: 100 }]
const comparisonColumns = [{ colKey: 'metric', title: '指标', cell: 'metric' }, { colKey: 'score', title: '基线 → 候选', cell: 'score' }, { colKey: 'delta', title: '绝对差值', cell: 'delta' }, { colKey: 'comparable_sample_count', title: '可比较样本' }, { colKey: 'improved', title: '结论', cell: 'improved' }]
const chatModels = computed(() => models.value.filter(m => m.type === 'KnowledgeQA'))
const rerankModels = computed(() => models.value.filter(m => m.type === 'Rerank'))
const completedRuns = computed(() => runs.value.filter(r => r.status === 'completed'))
const metricOptions = computed(() => metrics.value.map(m => ({ label: `${m.name} (${m.version})`, value: m.name })))

async function loadDatasets() { loadingDatasets.value = true; try { datasets.value = (await listEvaluationDatasets()).data } catch (e: any) { MessagePlugin.error(e.message || '加载数据集失败') } finally { loadingDatasets.value = false } }
async function loadRuns() { loadingRuns.value = true; try { runs.value = (await listEvaluationRuns()).data } catch (e: any) { MessagePlugin.error(e.message || '加载运行失败') } finally { loadingRuns.value = false } }
async function loadSamples() { if (!selectedDataset.value) return; loadingSamples.value = true; try { samples.value = (await listEvaluationSamples(selectedDataset.value.id)).data } finally { loadingSamples.value = false } }
async function loadOptions() { const [metricList, kbResponse, modelList] = await Promise.all([listEvaluationMetrics(), listKnowledgeBases(), listModels()]); metrics.value = metricList; knowledgeBases.value = (kbResponse as any).data || []; models.value = modelList; runForm.metric_names = metricList.map(m => m.name) }
function selectDataset({ row }: { row: EvaluationDataset }) { selectedDataset.value = row; void loadSamples() }
function selectResult({ row }: { row: EvaluationRunResult }) { selectedResult.value = row }
function openDatasetDialog(row?: EvaluationDataset) { editingDataset.value = row || null; datasetForm.name = row?.name || ''; datasetForm.description = row?.description || ''; datasetDialogVisible.value = true }
async function saveDataset() { if (!datasetForm.name.trim()) return MessagePlugin.warning('请输入数据集名称'); saving.value = true; try { if (editingDataset.value) await updateEvaluationDataset(editingDataset.value.id, datasetForm); else await createEvaluationDataset(datasetForm); datasetDialogVisible.value = false; await loadDatasets(); MessagePlugin.success('数据集已保存') } catch (e: any) { MessagePlugin.error(e.message || '保存失败') } finally { saving.value = false } }
async function removeDataset(row: EvaluationDataset) { try { await deleteEvaluationDataset(row.id); if (selectedDataset.value?.id === row.id) { selectedDataset.value = null; samples.value = [] }; await loadDatasets(); MessagePlugin.success('数据集已删除') } catch (e: any) { MessagePlugin.error(e.message || '删除失败') } }
function openSampleDialog(row?: EvaluationSample) { if (!selectedDataset.value) return; editingSample.value = row || null; sampleForm.question = row?.question || ''; sampleForm.reference_answer = row?.reference_answer || ''; sampleContextsText.value = JSON.stringify(row?.reference_contexts || [], null, 2); sampleDialogVisible.value = true }
async function saveSample() { if (!selectedDataset.value || !sampleForm.question.trim() || !sampleForm.reference_answer.trim()) return MessagePlugin.warning('问题和参考答案不能为空'); let contexts: ReferenceContext[]; try { contexts = JSON.parse(sampleContextsText.value || '[]'); if (!Array.isArray(contexts)) throw new Error() } catch { return MessagePlugin.warning('参考上下文必须是 JSON 数组') }; saving.value = true; try { const payload = { ...sampleForm, reference_contexts: contexts }; if (editingSample.value) await updateEvaluationSample(selectedDataset.value.id, editingSample.value.id, payload); else await createEvaluationSample(selectedDataset.value.id, payload); sampleDialogVisible.value = false; await Promise.all([loadSamples(), loadDatasets()]); MessagePlugin.success('样本已保存') } catch (e: any) { MessagePlugin.error(e.message || '保存失败') } finally { saving.value = false } }
async function removeSample(row: EvaluationSample) { if (!selectedDataset.value) return; try { await deleteEvaluationSample(selectedDataset.value.id, row.id); await Promise.all([loadSamples(), loadDatasets()]); MessagePlugin.success('样本已删除') } catch (e: any) { MessagePlugin.error(e.message || '删除失败') } }
function openRunDialog() { if (!runForm.dataset_id && datasets.value.length) runForm.dataset_id = datasets.value[0].id; if (!runForm.chat_model_id && chatModels.value.length) runForm.chat_model_id = chatModels.value[0].id || ''; runDialogVisible.value = true }
async function saveRun() { if (!runForm.dataset_id || !runForm.knowledge_base_id || !runForm.chat_model_id) return MessagePlugin.warning('请选择数据集、知识库和问答模型'); if (!runForm.metric_names.length) return MessagePlugin.warning('至少选择一个指标'); saving.value = true; try { await createEvaluationRun({ ...runForm, metrics: runForm.metric_names.map(name => ({ name, version: metrics.value.find(m => m.name === name)?.version || 'v1' })), metric_names: undefined }); runDialogVisible.value = false; activeTab.value = 'runs'; await loadRuns(); MessagePlugin.success('评估运行已创建') } catch (e: any) { MessagePlugin.error(e.message || '创建运行失败') } finally { saving.value = false } }
async function openRunDetail(payload: { row: EvaluationRun }) { selectedRun.value = payload.row; selectedResult.value = null; runDrawerVisible.value = true; await loadRunResults(payload.row.id) }
async function loadRunResults(runId: string) { loadingRunResults.value = true; try { runResults.value = (await listEvaluationRunResults(runId)).data } finally { loadingRunResults.value = false } }
async function compareRuns() { if (!baselineRunId.value || !candidateRunId.value) return MessagePlugin.warning('请选择基线运行和候选运行'); comparing.value = true; try { comparison.value = await compareEvaluationRuns(baselineRunId.value, candidateRunId.value) } catch (e: any) { MessagePlugin.error(e.message || '对比失败') } finally { comparing.value = false } }
function statusText(status: EvaluationRun['status']) { return ({ pending: '等待中', running: '运行中', completed: '已完成', failed: '失败' } as const)[status] }
function statusTheme(status: EvaluationRun['status']) { return status === 'completed' ? 'success' : status === 'failed' ? 'danger' : status === 'running' ? 'primary' : 'default' }
function progress(run: EvaluationRun) { return run.total_samples ? Math.round(run.finished_samples * 100 / run.total_samples) : 0 }
function formatScore(value: number) { return Number(value).toFixed(4) }
function signedScore(value: number) { return `${value > 0 ? '+' : ''}${formatScore(value)}` }
function metricScoreList(scores: Record<string, MetricScore>) { return Object.values(scores || {}) }
function runLabel(run: EvaluationRun) { return `${run.dataset_name} · ${run.id.slice(0, 8)}` }

let pollTimer: number | undefined
onMounted(async () => { await Promise.all([loadDatasets(), loadRuns(), loadOptions()]); pollTimer = window.setInterval(async () => { if (runs.value.some(r => r.status === 'pending' || r.status === 'running')) { await loadRuns(); if (selectedRun.value && (selectedRun.value.status === 'pending' || selectedRun.value.status === 'running')) { selectedRun.value = await getEvaluationRun(selectedRun.value.id); await loadRunResults(selectedRun.value.id) } } }, 2500) })
onBeforeUnmount(() => { if (pollTimer) window.clearInterval(pollTimer) })
</script>

<style scoped lang="less">
.evaluation-page { height: 100%; overflow: auto; padding: 28px 32px; background: var(--td-bg-color-page); color: var(--td-text-color-primary); }
.page-heading { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 20px; h1 { margin: 0 0 8px; font-size: 26px; } p { margin: 0; color: var(--td-text-color-secondary); } }
.evaluation-tabs { background: transparent; }
.panel { margin-top: 18px; padding: 20px; border: 1px solid var(--td-component-stroke); border-radius: 10px; background: var(--td-bg-color-container); }
.dataset-layout { display: grid; grid-template-columns: minmax(340px, 0.8fr) minmax(520px, 1.4fr); gap: 18px; }.dataset-panel,.sample-panel{min-width:0}.panel-heading{display:flex;align-items:center;justify-content:space-between;margin-bottom:16px}h2{margin:0 0 4px;font-size:18px}h3{margin:24px 0 12px}.muted,.field-hint{color:var(--td-text-color-secondary);font-size:12px}.cell-primary{margin-bottom:4px}.clamp{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.form-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 18px}.config-grid{grid-template-columns:repeat(5,1fr)}.compare-controls{display:grid;grid-template-columns:1fr auto 1fr auto;align-items:center;gap:12px;margin-bottom:20px}.compare-arrow{color:var(--td-text-color-placeholder)}.metric-version{margin-left:8px;color:var(--td-text-color-placeholder);font-size:12px}.improved{color:var(--td-success-color);font-weight:600}.declined{color:var(--td-error-color)}.run-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:18px}.run-summary>div{padding:14px;border-radius:8px;background:var(--td-bg-color-secondarycontainer);display:flex;flex-direction:column;gap:8px}.run-summary span{font-size:12px;color:var(--td-text-color-secondary)}.run-summary code{overflow:hidden;text-overflow:ellipsis}.metric-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:12px}.metric-card{padding:14px;border:1px solid var(--td-component-stroke);border-radius:8px;display:flex;flex-direction:column;gap:6px}.metric-card strong{font-size:22px}.metric-card span,.metric-card small{color:var(--td-text-color-secondary);font-size:12px}.result-detail{margin-top:18px;padding-top:8px;border-top:1px solid var(--td-component-stroke)}.evidence-grid{display:grid;grid-template-columns:1fr 1fr;gap:24px}.evidence-grid p{white-space:pre-wrap;line-height:1.65}.evidence-grid ol{padding-left:22px;max-height:480px;overflow:auto}.evidence-grid li{margin-bottom:14px;border-bottom:1px solid var(--td-component-stroke)}.snapshot{margin:16px 0;padding:12px 14px;border:1px solid var(--td-component-stroke);border-radius:8px}.snapshot summary{cursor:pointer;font-weight:600}.snapshot pre{max-height:320px;overflow:auto;margin:12px 0 0;padding:12px;border-radius:6px;background:var(--td-bg-color-secondarycontainer);font-size:12px}
@media (max-width: 1100px){.dataset-layout,.evidence-grid{grid-template-columns:1fr}.config-grid{grid-template-columns:repeat(2,1fr)}.run-summary{grid-template-columns:repeat(2,1fr)}}
</style>
