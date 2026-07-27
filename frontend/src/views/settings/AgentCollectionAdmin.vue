<template>
  <div class="collection-admin">
    <header><div><h2>用户信息采集</h2><p>查看所有智能体为用户收集的最新信息和变更历史。</p></div>
      <t-dropdown :options="exportOptions" @click="startExport($event.value as 'csv' | 'xlsx')">
        <t-button variant="outline"><template #icon><t-icon name="download" /></template>导出</t-button>
      </t-dropdown>
    </header>
    <div class="summary">
      <div><span>用户数</span><strong>{{ data.summary.users }}</strong></div>
      <div><span>采集记录</span><strong>{{ data.summary.profiles }}</strong></div>
      <div><span>今日更新</span><strong>{{ data.summary.updated_today }}</strong></div>
      <div><span>待补充</span><strong>{{ data.summary.incomplete }}</strong></div>
    </div>
    <div class="filters">
      <t-input v-model="filter.keyword" clearable placeholder="搜索用户或智能体 ID" @enter="load" />
      <t-input-number v-model="filter.tenant_id" :min="1" placeholder="租户 ID" />
      <t-input v-model="filter.agent_id" clearable placeholder="智能体 ID" />
      <t-input v-model="filter.user_id" clearable placeholder="用户 ID" />
      <t-input v-model="filter.field_key" clearable placeholder="字段标识" />
      <t-input v-model="filter.field_value" clearable placeholder="字段值" />
      <t-date-picker v-model="updatedFrom" clearable placeholder="更新开始日期" />
      <t-date-picker v-model="updatedTo" clearable placeholder="更新结束日期" />
      <t-select v-model="completion" :options="completionOptions" @change="load" />
      <t-button theme="primary" @click="resetAndLoad"><template #icon><t-icon name="search" /></template>查询</t-button>
    </div>
    <t-table class="collection-table" row-key="id" :data="data.items" :columns="columns" :loading="loading" hover @row-click="openDetail($event.row)">
      <template #agent="{ row }"><div class="primary">{{ row.agent_name || row.agent_id }}</div><small>{{ row.agent_id }}</small></template>
      <template #user="{ row }"><div class="primary">{{ row.user_id }}</div><small>租户 {{ row.tenant_id }}</small><div class="mobile-profile-summary"><span>{{ valueSummary(row) }}</span><small>{{ formatTime(row.updated_at) }}</small></div></template>
      <template #progress="{ row }"><t-tag :theme="row.is_complete ? 'success' : 'warning'">{{ row.completed_required }}/{{ row.required_total }}</t-tag></template>
      <template #values="{ row }">{{ valueSummary(row) }}</template>
      <template #updated="{ row }">{{ formatTime(row.updated_at) }}</template>
      <template #actions="{ row }"><t-button size="small" variant="text" @click.stop="openDetail(row)">查看</t-button></template>
    </t-table>
    <t-pagination v-model="filter.page" v-model:page-size="filter.page_size" :total="data.total" :page-size-options="[20, 50, 100]" @change="load" />

    <t-drawer v-model:visible="detailVisible" size="min(840px, calc(100vw - 16px))" header="采集详情" :footer="false">
      <div v-if="detail" class="detail">
        <div class="detail-meta"><span>用户 {{ detail.user_id }}</span><span>智能体 {{ detail.agent_name }}</span><span>租户 {{ detail.tenant_id }}</span></div>
        <t-tabs v-model="detailTab">
          <t-tab-panel value="latest" label="最新信息">
            <div v-for="field in detail.fields" :key="field.key" class="value-row">
              <div><strong>{{ field.label }}</strong><small>{{ field.key }}</small></div>
              <span>{{ displayValue(detail.values[field.key]?.value) }}</span>
              <t-button shape="square" variant="text" title="编辑" @click="beginEdit(field.key)"><t-icon name="edit" /></t-button>
            </div>
            <div v-if="!detail.fields.length" class="empty">暂无字段</div>
            <template v-if="Object.keys(detail.inactive_values || {}).length">
              <h4>已停用字段</h4>
              <div v-for="(entry, key) in detail.inactive_values" :key="key" class="value-row inactive">
                <div><strong>{{ key }}</strong><small>已停用</small></div><span>{{ displayValue(entry.value) }}</span>
              </div>
            </template>
          </t-tab-panel>
          <t-tab-panel value="history" label="变更历史">
            <t-timeline v-if="history.length" class="collection-history">
              <t-timeline-item v-for="item in history" :key="item.id" :label="formatTime(item.created_at)">
                <strong>{{ fieldLabel(item.field_key) }}</strong>：{{ displayValue(item.new_value) }}
                <p>{{ sourceLabel(item.source) }}<template v-if="item.change_reason"> · {{ item.change_reason }}</template></p>
              </t-timeline-item>
            </t-timeline>
            <div v-else class="empty">暂无变更记录</div>
            <t-pagination v-if="historyTotal > 20" v-model="historyPage" :total="historyTotal" :page-size="20" @change="loadHistory" />
          </t-tab-panel>
        </t-tabs>
        <t-button theme="danger" variant="outline" @click="openPurge"><template #icon><t-icon name="delete" /></template>彻底删除</t-button>
      </div>
    </t-drawer>

    <t-dialog v-model:visible="editVisible" header="修订采集信息" :confirm-btn="{ content: '保存', loading: mutating }" @confirm="saveEdit">
      <div class="dialog-form"><label>新的值</label><t-textarea v-model="editValue" autosize /><label>修改原因</label><t-textarea v-model="editReason" autosize placeholder="必填，将写入审计历史" /></div>
    </t-dialog>
    <t-dialog v-model:visible="purgeVisible" header="彻底删除采集数据" theme="danger" confirm-btn="永久删除" @confirm="purge">
      <div class="dialog-form"><p>此操作会删除最新值和全部历史，且无法恢复。</p><label>输入记录 ID 确认</label><t-input v-model="purgeConfirmation" :placeholder="detail?.id" /><label>删除原因</label><t-textarea v-model="purgeReason" autosize /></div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { AgentCollectionProfile, CollectionFilter, CollectionHistory } from '@/api/system/agent-collection'
import { createCollectionExport, downloadCollectionExport, getCollectionExport, getCollectionProfile, listCollectionHistory, listCollectionProfiles, purgeCollectionProfile, updateCollectionField } from '@/api/system/agent-collection'
import { cleanCollectionFilter, collectionDateRange, collectionExportFilename, displayCollectionValue, orderedCollectionFields } from './agentCollectionAdmin'

const emptyData = () => ({ items: [], total: 0, page: 1, page_size: 20, summary: { users: 0, profiles: 0, updated_today: 0, incomplete: 0 } })
const filter = reactive<CollectionFilter>({ keyword: '', agent_id: '', user_id: '', page: 1, page_size: 20 })
const data = reactive(emptyData())
const loading = ref(false), detailVisible = ref(false), editVisible = ref(false), purgeVisible = ref(false), mutating = ref(false)
const detail = ref<AgentCollectionProfile>(), history = ref<CollectionHistory[]>([]), detailTab = ref('latest')
const completion = ref('all'), editKey = ref(''), editValue = ref(''), editReason = ref('')
const updatedFrom = ref(''), updatedTo = ref(''), historyPage = ref(1), historyTotal = ref(0)
const purgeConfirmation = ref(''), purgeReason = ref('')
const completionOptions = [{ value: 'all', label: '全部状态' }, { value: 'complete', label: '已完成' }, { value: 'incomplete', label: '待补充' }]
const exportOptions = [{ value: 'csv', content: '导出 CSV' }, { value: 'xlsx', content: '导出 XLSX' }]
const columns = [
  { colKey: 'agent', title: '智能体', width: 155 }, { colKey: 'user', title: '用户', width: 155 },
  { colKey: 'progress', title: '必填进度', width: 85 }, { colKey: 'values', title: '采集内容', width: 220, ellipsis: true },
  { colKey: 'updated', title: '最近更新', width: 150 },
  { colKey: 'actions', title: '', width: 52 },
]

async function load() {
  loading.value = true
  try {
    filter.complete = completion.value === 'all' ? undefined : completion.value === 'complete'
    Object.assign(filter, collectionDateRange(updatedFrom.value, updatedTo.value))
    Object.assign(data, await listCollectionProfiles(cleanCollectionFilter(filter)))
  } catch (error: any) { MessagePlugin.error(error?.message || '加载失败') } finally { loading.value = false }
}
function resetAndLoad() { filter.page = 1; void load() }
async function openDetail(row: AgentCollectionProfile) {
  detailVisible.value = true; detailTab.value = 'latest'
  try {
    const [profile, page] = await Promise.all([getCollectionProfile(row.id), listCollectionHistory(row.id)])
    profile.fields = orderedCollectionFields(profile.fields); detail.value = profile
    history.value = page.items ?? []; historyTotal.value = page.total; historyPage.value = 1
  } catch (error: any) { MessagePlugin.error(error?.message || '加载详情失败') }
}
function beginEdit(key: string) { editKey.value = key; editValue.value = displayValue(detail.value?.values[key]?.value); editReason.value = ''; editVisible.value = true }
async function saveEdit() {
  if (!detail.value || !editReason.value.trim()) { MessagePlugin.warning('请填写修改原因'); return }
  mutating.value = true
  const value = editTypedValue()
  if (value === undefined) return
  try { await updateCollectionField(detail.value.id, editKey.value, value, editReason.value); editVisible.value = false; await openDetail(detail.value); await load(); MessagePlugin.success('已更新') }
  catch (error: any) { MessagePlugin.error(error?.message || '更新失败') } finally { mutating.value = false }
}
function openPurge() { purgeConfirmation.value = ''; purgeReason.value = ''; purgeVisible.value = true }
async function purge() {
  if (!detail.value) return
  if (purgeConfirmation.value !== detail.value.id || !purgeReason.value.trim()) { MessagePlugin.warning('请输入记录 ID 和删除原因'); return }
  try { await purgeCollectionProfile(detail.value.id, purgeReason.value); purgeVisible.value = false; detailVisible.value = false; await load(); MessagePlugin.success('已彻底删除') }
  catch (error: any) { MessagePlugin.error(error?.message || '删除失败') }
}
async function startExport(format: 'csv' | 'xlsx') {
  try {
    const exportFilter = cleanCollectionFilter({ ...filter, ...collectionDateRange(updatedFrom.value, updatedTo.value) })
    const task = await createCollectionExport(format, exportFilter); MessagePlugin.info('正在生成导出文件')
    for (let count = 0; count < 60; count += 1) {
      await new Promise((resolve) => setTimeout(resolve, 1000)); const current = await getCollectionExport(task.id)
      if (current.status === 'completed') { await downloadCollectionExport(task.id, collectionExportFilename(current.filename, format)); return }
      if (current.status === 'failed') throw new Error(current.error_message || '导出失败')
    }
    throw new Error('导出超时，请稍后重试')
  } catch (error: any) { MessagePlugin.error(error?.message || '导出失败') }
}
async function loadHistory() { if (!detail.value) return; const page = await listCollectionHistory(detail.value.id, historyPage.value); history.value = page.items; historyTotal.value = page.total }
function valueSummary(profile: AgentCollectionProfile) { return profile.fields.slice(0, 3).map((field) => `${field.label}: ${displayValue(profile.values[field.key]?.value)}`).join('；') || '暂无' }
function displayValue(value: unknown) { return displayCollectionValue(value) }
function editTypedValue(): unknown {
  const type = detail.value?.fields.find((field) => field.key === editKey.value)?.type
  if (type === 'number') {
    const value = Number(editValue.value)
    if (!Number.isFinite(value)) { MessagePlugin.warning('请输入有效数字'); return undefined }
    return value
  }
  if (type === 'multiple_choice') return editValue.value.split(/[，,]/).map((value) => value.trim()).filter(Boolean)
  return editValue.value
}
function fieldLabel(key: string) { return detail.value?.fields.find((field) => field.key === key)?.label || key }
function sourceLabel(source: string) { return ({ structured_answer: '结构化回答', message_extraction: '自然语言提取', system_admin: '管理员修订', schema_migration: '配置迁移' } as Record<string, string>)[source] || source }
function formatTime(value: string) { return value ? new Date(value).toLocaleString() : '-' }
onMounted(load)
</script>

<style scoped lang="less">
.collection-admin { display: grid; gap: 18px; min-width: 0; } header { display: flex; justify-content: space-between; align-items: start; } h2 { margin: 0 0 6px; } header p, small { color: var(--td-text-color-secondary); } .summary { display: grid; grid-template-columns: repeat(4, 1fr); border: 1px solid var(--td-component-stroke); border-radius: 6px; } .summary div { padding: 14px 18px; display: grid; gap: 6px; } .summary div + div { border-left: 1px solid var(--td-component-stroke); } .summary strong { font-size: 24px; } .filters { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 8px; } .primary { font-weight: 500; } .mobile-profile-summary { display: none; } .detail { display: grid; gap: 18px; } .detail-meta { display: flex; flex-wrap: wrap; gap: 8px 18px; color: var(--td-text-color-secondary); } .value-row { display: grid; grid-template-columns: 150px minmax(0, 1fr) 32px; align-items: center; gap: 12px; padding: 12px 0; border-bottom: 1px solid var(--td-component-stroke); } .value-row.inactive { grid-template-columns: 150px 1fr; opacity: .72; } .value-row div { display: grid; } .dialog-form { display: grid; gap: 8px; } .empty { padding: 32px; text-align: center; color: var(--td-text-color-placeholder); } :deep(.t-pagination) { justify-content: flex-end; } @media (max-width: 800px) { .filters { grid-template-columns: 1fr; } .summary { grid-template-columns: 1fr; } .summary div + div { border-left: 0; border-top: 1px solid var(--td-component-stroke); } .mobile-profile-summary { display: grid; gap: 2px; margin-top: 5px; font-size: 12px; color: var(--td-text-color-secondary); } :deep(.collection-table col:nth-child(4)), :deep(.collection-table col:nth-child(5)), :deep(.collection-table th:nth-child(4)), :deep(.collection-table th:nth-child(5)), :deep(.collection-table td:nth-child(4)), :deep(.collection-table td:nth-child(5)) { display: none; } :deep(.collection-table table) { width: 100% !important; min-width: 0 !important; table-layout: fixed; } }

:deep(.collection-history .t-timeline-item) {
  display: grid;
  grid-template-columns: 152px 8px minmax(0, 1fr);
}

:deep(.collection-history .t-timeline-item__label) {
  position: static;
  min-width: 0;
  padding-right: 16px;
  overflow-wrap: anywhere;
  text-align: left;
}

:deep(.collection-history .t-timeline-item__wrapper) {
  margin-left: 0 !important;
}

:deep(.collection-history .t-timeline-item__content) {
  min-width: 0;
}
</style>
