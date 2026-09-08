<template>
  <Teleport to="body">
    <PublishNoteDialog v-model:visible="noteVisible" :loading="publishing" @confirm="publish" />
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="emit('update:visible', false)">
        <div class="settings-modal">
          <button class="close-btn" @click="emit('update:visible', false)" aria-label="close">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </button>

          <div class="settings-container">
            <!-- 左侧导航: 对齐新建知识库 -->
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ model?.name || t('semantic.model.add') }}</h2>
              </div>
              <div class="settings-nav">
                <template v-for="group in navGroups" :key="group.key">
                  <div class="nav-group-title">{{ group.label }}</div>
                  <div v-for="item in group.items" :key="item.key"
                    :class="['nav-item', { active: currentSection === item.key }]"
                    @click="currentSection = item.key">
                    <t-icon :name="item.icon" class="nav-icon" />
                    <span class="nav-label">{{ item.label }}</span>
                  </div>
                </template>
              </div>
            </div>

            <!-- 右侧内容 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <div v-if="model" class="section-content">
                  <!-- 基本信息 -->
                  <div v-show="currentSection === 'basic'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.basic') }}</h3>
                      <p class="section-desc">{{ $t('semantic.agent.modelScopeDesc') }}</p>
                    </div>
                    <t-form label-width="110px" label-align="right" size="small">
                      <t-form-item :label="t('semantic.model.name')">
                        <t-input v-model="form.name" :disabled="!canEdit || isPublished" @change="syncNameToDoc" />
                      </t-form-item>
                      <t-form-item :label="t('semantic.model.titleField')">
                        <t-input v-model="form.title" :disabled="!canEdit" />
                      </t-form-item>
                      <t-form-item :label="t('semantic.model.connection')">
                        <t-select v-model="form.connection_id" :disabled="!canEdit" @change="syncDataSource">
                          <t-option v-for="c in connections" :key="c.id" :value="c.id" :label="`${c.title || c.name} (${c.type})`" />
                        </t-select>
                      </t-form-item>
                      <t-form-item :label="t('semantic.model.description')">
                        <t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }" :disabled="!canEdit" />
                      </t-form-item>
                    </t-form>
                  </div>

                  <!-- 维度 -->
                  <div v-show="currentSection === 'dimensions'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.dimensions') }}</h3>
                      <p class="section-desc">{{ $t('semantic.model.editor.dimensionsDesc') }}</p>
                    </div>
                    <div class="section-block">
                      <div class="section-block-header">
                        <t-button size="small" variant="outline" :disabled="!canEdit" @click="addMember('dimensions')">
                          {{ t('semantic.model.editor.addMember') }}
                        </t-button>
                      </div>
                      <t-table row-key="_rowKey" :data="dimensionRows" :columns="memberColumns" size="small" max-height="420">
                        <template #name="{ row }"><t-input v-model="row.name" size="small" :disabled="!canEdit" /></template>
                        <template #sql="{ row }"><t-input v-model="row.sql" size="small" :disabled="!canEdit" /></template>
                        <template #type="{ row }">
                          <t-select v-model="row.type" size="small" :disabled="!canEdit">
                            <t-option v-for="tp in dimensionTypes" :key="tp" :value="tp" :label="tp" />
                          </t-select>
                        </template>
                        <template #title="{ row }"><t-input v-model="row.title" size="small" :disabled="!canEdit" /></template>
                        <template #description="{ row }"><t-input v-model="row.description" size="small" :disabled="!canEdit" /></template>
                        <template #primaryKey="{ row }"><t-checkbox v-model="row.primaryKey" :disabled="!canEdit" /></template>
                        <template #op="{ row }">
                          <t-button size="small" variant="text" theme="danger" :disabled="!canEdit" @click="removeMember('dimensions', row)">
                            {{ t('semantic.model.editor.remove') }}
                          </t-button>
                        </template>
                      </t-table>
                    </div>
                  </div>

                  <!-- 指标 -->
                  <div v-show="currentSection === 'measures'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.measures') }}</h3>
                      <p class="section-desc">{{ $t('semantic.model.editor.measuresDesc') }}</p>
                    </div>
                    <div class="section-block">
                      <div class="section-block-header">
                        <t-button size="small" variant="outline" :disabled="!canEdit" @click="addMember('measures')">
                          {{ t('semantic.model.editor.addMember') }}
                        </t-button>
                      </div>
                      <t-table row-key="_rowKey" :data="measureRows" :columns="memberColumns" size="small" max-height="420">
                        <template #name="{ row }"><t-input v-model="row.name" size="small" :disabled="!canEdit" /></template>
                        <template #sql="{ row }"><t-input v-model="row.sql" size="small" :disabled="!canEdit" /></template>
                        <template #type="{ row }">
                          <t-select v-model="row.type" size="small" :disabled="!canEdit">
                            <t-option v-for="tp in measureTypes" :key="tp" :value="tp" :label="tp" />
                          </t-select>
                        </template>
                        <template #title="{ row }"><t-input v-model="row.title" size="small" :disabled="!canEdit" /></template>
                        <template #description="{ row }"><t-input v-model="row.description" size="small" :disabled="!canEdit" /></template>
                        <template #primaryKey="{ row }"><span /></template>
                        <template #op="{ row }">
                          <t-button size="small" variant="text" theme="danger" :disabled="!canEdit" @click="removeMember('measures', row)">
                            {{ t('semantic.model.editor.remove') }}
                          </t-button>
                        </template>
                      </t-table>
                    </div>
                  </div>

                  <!-- 关联 -->
                  <div v-show="currentSection === 'joins'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.joins') }}</h3>
                      <p class="section-desc">{{ $t('semantic.model.editor.joinsDesc') }}</p>
                    </div>
                    <div class="section-block">
                      <div class="section-block-header">
                        <t-button size="small" variant="outline" :disabled="!canEdit" @click="addJoin">
                          {{ t('semantic.model.editor.addMember') }}
                        </t-button>
                      </div>
                      <t-table row-key="_rowKey" :data="joinRows" :columns="joinColumns" size="small" max-height="420">
                        <template #name="{ row }"><t-input v-model="row.name" size="small" :disabled="!canEdit" /></template>
                        <template #sql="{ row }"><t-input v-model="row.sql" size="small" :disabled="!canEdit" /></template>
                        <template #relationship="{ row }">
                          <t-select v-model="row.relationship" size="small" :disabled="!canEdit">
                            <t-option v-for="r in relationships" :key="r" :value="r" :label="r" />
                          </t-select>
                        </template>
                        <template #op="{ row }">
                          <t-button size="small" variant="text" theme="danger" :disabled="!canEdit" @click="removeJoin(row)">
                            {{ t('semantic.model.editor.remove') }}
                          </t-button>
                        </template>
                      </t-table>
                    </div>
                  </div>

                  <!-- 数据权限 -->
                  <div v-show="currentSection === 'groups'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.groups') }}</h3>
                      <p class="section-desc">{{ $t('semantic.model.editor.groupsDesc') }}</p>
                    </div>
                    <t-form label-width="110px" label-align="right" size="small">
                      <t-form-item :label="t('semantic.model.editor.groups')">
                        <t-select v-model="form.allowed_groups" multiple filterable :disabled="!canEdit" clearable>
                          <t-option v-for="g in groups" :key="g.id" :value="g.name" :label="g.title || g.name" />
                        </t-select>
                      </t-form-item>
                    </t-form>
                    <div class="groups-hint">{{ t('semantic.model.editor.groupsHint') }}</div>
                  </div>

                  <!-- YAML 源码 -->
                  <div v-show="currentSection === 'yaml'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.yamlMode') }}</h3>
                      <p class="section-desc">{{ $t('semantic.model.editor.yamlDesc') }}</p>
                    </div>
                    <t-textarea v-model="yamlText" class="yaml-editor" :autosize="{ minRows: 16, maxRows: 26 }"
                      :disabled="!canEdit" spellcheck="false" />
                    <div class="yaml-actions">
                      <t-button size="small" variant="outline" :disabled="!canEdit || !isCube" @click="applyYaml">
                        {{ t('semantic.model.editor.applyYaml') }}
                      </t-button>
                    </div>
                  </div>

                  <!-- 数据预览 -->
                  <div v-show="currentSection === 'preview'" class="section">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('semantic.model.editor.preview') }}</h3>
                      <p class="section-desc">{{ $t('semantic.model.editor.previewDesc') }}</p>
                    </div>
                    <div class="preview-controls">
                      <t-select v-model="previewMeasures" multiple size="small" :placeholder="t('semantic.model.editor.measures')" :disabled="!isPublished" class="preview-select">
                        <t-option v-for="m in cube?.measures || []" :key="m.name" :value="previewName(m.name)" :label="m.name" />
                      </t-select>
                      <t-select v-model="previewDimensions" multiple size="small" :placeholder="t('semantic.model.editor.dimensions')" :disabled="!isPublished" class="preview-select">
                        <t-option v-for="d in cube?.dimensions || []" :key="d.name" :value="previewName(d.name)" :label="d.name" />
                      </t-select>
                      <t-button size="small" theme="primary" :loading="previewing" :disabled="!isPublished" @click="runPreview">
                        {{ t('semantic.model.editor.previewRun') }}
                      </t-button>
                    </div>
                    <div v-if="!isPublished" class="preview-hint">{{ t('semantic.model.editor.previewPublishFirst') }}</div>
                    <div v-else-if="previewHint" class="preview-hint denied">{{ previewHint }}</div>
                    <t-table v-if="previewRows.length" row-key="_idx" :data="previewRows" size="small" max-height="360" :columns="previewColumns" />
                    <div v-else-if="previewRan" class="preview-hint">{{ t('semantic.model.editor.previewNoData') }}</div>
                  </div>

                  <div class="editor-error" v-if="saveError">{{ saveError }}</div>
                </div>
              </div>

              <!-- 底部操作条: 对齐新建知识库 -->
              <div class="editor-footer">
                <span class="footer-hint">{{ t('semantic.model.publishHint') }}</span>
                <div class="footer-actions">
                  <t-button variant="default" @click="emit('update:visible', false)">{{ t('semantic.group.cancel') }}</t-button>
                  <t-button v-if="canEdit" variant="outline" :loading="saving" @click="save">{{ t('semantic.model.editor.save') }}</t-button>
                  <t-button v-if="canPublish" theme="primary" :loading="publishing" @click="noteVisible = true">{{ t('semantic.model.publish') }}</t-button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import yaml from 'js-yaml'
import PublishNoteDialog from './PublishNoteDialog.vue'
import {
  getModel,
  updateModel,
  publishModel,
  previewModel,
  type ConnectionInfo,
  type DataGroup,
  type SemanticModel
} from './api'

interface MemberRow {
  _rowKey: number
  name: string
  sql: string
  type: string
  title: string
  description: string
  primaryKey: boolean
}
interface JoinRow {
  _rowKey: number
  name: string
  sql: string
  relationship: string
}

const props = defineProps<{
  visible: boolean
  model: SemanticModel | null
  connections: ConnectionInfo[]
  groups: DataGroup[]
  canEdit: boolean
  canPublish: boolean
}>()
const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void
  (e: 'saved', m: SemanticModel): void
  (e: 'published'): void
}>()

const { t } = useI18n()

const visible = computed({
  get: () => props.visible,
  set: v => emit('update:visible', v)
})

const rowKeySeq = ref(0)
const currentSection = ref('basic')
const dimensionTypes = ['string', 'number', 'time', 'boolean']
const measureTypes = ['count', 'sum', 'avg', 'min', 'max', 'count_distinct', 'number']

const navGroups = computed(() => {
  const structure = [
    { key: 'dimensions', icon: 'layers', label: t('semantic.model.editor.dimensions') },
    { key: 'measures', icon: 'root-list', label: t('semantic.model.editor.measures') },
    { key: 'joins', icon: 'link', label: t('semantic.model.editor.joins') },
  ]
  const groups: { key: string; label: string; items: { key: string; icon: string; label: string }[] }[] = [
    {
      key: 'base',
      label: t('semantic.agentEditor.groupBase'),
      items: [
        { key: 'basic', icon: 'info-circle', label: t('semantic.model.editor.basic') },
        { key: 'groups', icon: 'usergroup', label: t('semantic.model.editor.groups') },
      ],
    },
  ]
  if (isCube.value) {
    groups.push({ key: 'structure', label: t('semantic.agentEditor.groupStructure'), items: structure })
  }
  groups.push({
    key: 'advanced',
    label: t('semantic.agentEditor.groupAdvanced'),
    items: [
      { key: 'yaml', icon: 'code', label: t('semantic.model.editor.yamlMode') },
      { key: 'preview', icon: 'play-circle', label: t('semantic.model.editor.preview') },
    ],
  })
  return groups
})
const model = ref<SemanticModel | null>(null)
const form = ref({
  name: '',
  title: '',
  description: '',
  connection_id: '',
  allowed_groups: [] as string[]
})
const mode = ref<'form' | 'yaml'>('form')
const yamlText = ref('')
const cube = ref<any>({ name: '', title: '', sql: '', data_source: '', measures: [], dimensions: [], joins: [] })
const saveError = ref('')
const saving = ref(false)
const publishing = ref(false)
const noteVisible = ref(false)
const previewMeasures = ref<string[]>([])
const previewDimensions = ref<string[]>([])
const previewRows = ref<Record<string, unknown>[]>([])
const previewColumns = ref<{ colKey: string; title: string }[]>([])
const previewing = ref(false)
const previewHint = ref('')
const previewRan = ref(false)

const isCube = computed(() => model.value?.kind !== 'view')
const isPublished = computed(() => model.value?.status === 'published')
const statusLabel = computed(() => {
  const s = model.value?.status
  if (s === 'published') return t('semantic.model.statusPublished')
  if (s === 'publish_failed') return t('semantic.model.statusFailed')
  return t('semantic.model.statusDraft')
})

const memberSections = computed(() => [
  {
    key: 'dimensions' as const,
    title: t('semantic.model.editor.dimensions'),
    rows: dimensionRows.value,
    types: ['string', 'number', 'time', 'boolean']
  },
  {
    key: 'measures' as const,
    title: t('semantic.model.editor.measures'),
    rows: measureRows.value,
    types: ['count', 'sum', 'avg', 'min', 'max', 'count_distinct', 'number']
  }
])
const dimensionRows = ref<MemberRow[]>([])
const measureRows = ref<MemberRow[]>([])
const joinRows = ref<JoinRow[]>([])
const relationships = ['one_to_one', 'one_to_many', 'many_to_one', 'many_to_many']

const memberColumns = computed(() => [
  { colKey: 'name', title: t('semantic.model.editor.memberName'), width: 130 },
  { colKey: 'type', title: t('semantic.model.editor.memberType'), width: 120 },
  { colKey: 'sql', title: t('semantic.model.editor.memberSql'), minWidth: 140 },
  { colKey: 'title', title: t('semantic.model.editor.memberTitle'), width: 130 },
  { colKey: 'description', title: t('semantic.model.editor.memberDesc'), minWidth: 140 },
  { colKey: 'primaryKey', title: t('semantic.model.editor.primaryKey'), width: 70 },
  { colKey: 'op', title: '', width: 70 }
])
const joinColumns = computed(() => [
  { colKey: 'name', title: t('semantic.model.editor.memberName'), width: 150 },
  { colKey: 'sql', title: t('semantic.model.editor.memberSql'), minWidth: 300 },
  { colKey: 'relationship', title: t('semantic.model.editor.relationship'), width: 150 },
  { colKey: 'op', title: '', width: 70 }
])

function nextKey() {
  return ++rowKeySeq.value
}

function rowsToMembers(rows: MemberRow[]) {
  return rows.map(r => ({
    name: r.name,
    ...(r.type ? { type: r.type } : {}),
    ...(r.sql ? { sql: r.sql } : {}),
    ...(r.title ? { title: r.title } : {}),
    ...(r.description ? { description: r.description } : {}),
    ...(r.primaryKey ? { primary_key: true } : {})
  }))
}

function membersToRows(members: any[] | undefined): MemberRow[] {
  return (members || []).map(m => ({
    _rowKey: nextKey(),
    name: m.name || '',
    sql: m.sql || '',
    type: m.type || '',
    title: m.title || '',
    description: m.description || '',
    primaryKey: !!m.primary_key
  }))
}

// doc (解析结果) <-> 表单行 双向同步
function applyDocToForm(doc: any) {
  const c = doc.cubes?.[0] || doc.views?.[0] || {}
  cube.value = c
  form.value.name = c.name || ''
  form.value.title = c.title || model.value?.title || ''
  if (c.data_source) {
    const conn = props.connections.find(x => x.name === c.data_source)
    if (conn) form.value.connection_id = conn.id
  }
  dimensionRows.value = membersToRows(c.dimensions)
  measureRows.value = membersToRows(c.measures)
  joinRows.value = (c.joins || []).map((j: any) => ({
    _rowKey: nextKey(),
    name: j.name || '',
    sql: j.sql || '',
    relationship: j.relationship || 'many_to_one'
  }))
}

function buildDoc(): any {
  const c: any = { ...cube.value }
  c.name = form.value.name
  c.title = form.value.title
  if (form.value.description) c.description = form.value.description
  else delete c.description
  const conn = props.connections.find(x => x.id === form.value.connection_id)
  if (conn) c.data_source = conn.name
  c.dimensions = rowsToMembers(dimensionRows.value)
  c.measures = rowsToMembers(measureRows.value)
  c.joins = joinRows.value.map(j => ({ name: j.name, sql: j.sql, relationship: j.relationship }))
  return { cubes: [c] }
}

function docToYaml(doc: any): string {
  return yaml.dump(doc, { lineWidth: 200, noRefs: true })
}

watch(
  () => props.visible,
  async v => {
    if (!v || !props.model) return
    saveError.value = ''
    previewRows.value = []
    previewRan.value = false
    previewHint.value = ''
    previewMeasures.value = []
    previewDimensions.value = []
    currentSection.value = 'basic'
    model.value = await getModel(props.model.id)
    form.value = {
      name: model.value.name,
      title: model.value.title,
      description: model.value.description || '',
      connection_id: model.value.connection_id,
      allowed_groups: [...(model.value.allowed_groups || [])]
    }
    yamlText.value = model.value.draft_yaml || ''
    try {
      const doc = yaml.load(yamlText.value) as any
      applyDocToForm(doc)
      mode.value = isCube.value ? 'form' : 'yaml'
    } catch {
      mode.value = 'yaml'
    }
  }
)

function syncNameToDoc() {
  if (cube.value) cube.value.name = form.value.name
}

function syncDataSource() {
  const conn = props.connections.find(x => x.id === form.value.connection_id)
  if (conn && cube.value) cube.value.data_source = conn.name
}

function addMember(key: 'dimensions' | 'measures') {
  const row: MemberRow = { _rowKey: nextKey(), name: '', sql: '', type: key === 'dimensions' ? 'string' : 'count', title: '', description: '', primaryKey: false }
  ;(key === 'dimensions' ? dimensionRows : measureRows).value.push(row)
}
function removeMember(key: 'dimensions' | 'measures', row: MemberRow) {
  ;(key === 'dimensions' ? dimensionRows : measureRows).value = (
    key === 'dimensions' ? dimensionRows : measureRows
  ).value.filter(r => r._rowKey !== row._rowKey)
}
function addJoin() {
  joinRows.value.push({ _rowKey: nextKey(), name: '', sql: '', relationship: 'many_to_one' })
}
function removeJoin(row: JoinRow) {
  joinRows.value = joinRows.value.filter(r => r._rowKey !== row._rowKey)
}

function applyYaml() {
  try {
    const doc = yaml.load(yamlText.value) as any
    applyDocToForm(doc)
    if (doc?.cubes?.[0]?.name) form.value.name = doc.cubes[0].name
    saveError.value = ''
  } catch (e: any) {
    saveError.value = t('semantic.model.editor.yamlInvalid', { err: e?.message || '' })
  }
}

function currentYaml(): string {
  if (currentSection.value === 'yaml' || !isCube.value) return yamlText.value
  return docToYaml(buildDoc())
}

async function save(): Promise<SemanticModel | null> {
  saving.value = true
  saveError.value = ''
  try {
    const updated = await updateModel(model.value!.id, {
      title: form.value.title,
      description: form.value.description,
      connection_id: form.value.connection_id,
      kind: model.value!.kind,
      draft_yaml: currentYaml(),
      allowed_groups: form.value.allowed_groups
    })
    model.value = updated
    emit('saved', updated)
    MessagePlugin.success(t('semantic.model.editor.saved'))
    return updated
  } catch (e: any) {
    saveError.value = e?.response?.data?.error || 'save failed'
    return null
  } finally {
    saving.value = false
  }
}

async function publish(note: string) {
  publishing.value = true
  noteVisible.value = false
  try {
    const saved = await save()
    if (!saved) return
    const resp = await publishModel(saved.id, note)
    MessagePlugin.success(t('semantic.model.publishOk', { n: resp.version }))
    model.value = { ...model.value!, status: 'published', version: resp.version }
    emit('published')
  } catch (e: any) {
    saveError.value = t('semantic.model.publishFail', { err: e?.response?.data?.error || '' })
    emit('published')
  } finally {
    publishing.value = false
  }
}

function previewName(member: string) {
  return `${model.value?.name}.${member}`
}

async function runPreview() {
  previewing.value = true
  previewHint.value = ''
  try {
    const resp = await previewModel(model.value!.id, {
      measures: previewMeasures.value,
      dimensions: previewDimensions.value,
      limit: 100
    })
    previewRows.value = (resp.data || []).map((r, i) => ({ _idx: i, ...r }))
    const keys = Object.keys(previewRows.value[0] || {}).filter(k => k !== '_idx')
    previewColumns.value = keys.map(k => ({ colKey: k, title: k }))
    previewRan.value = true
    if (resp.hint) previewHint.value = resp.hint
  } catch (e: any) {
    previewHint.value = e?.response?.data?.error || 'preview failed'
    previewRan.value = true
  } finally {
    previewing.value = false
  }
}
</script>

<style scoped>
/* ---- 壳: 照抄 KnowledgeBaseEditorModal ---- */
.settings-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.settings-modal {
  position: relative;
  width: 90vw;
  max-width: 1000px;
  height: 85vh;
  max-height: 780px;
  background: var(--td-bg-color-container);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.close-btn {
  position: absolute;
  top: 20px;
  right: 20px;
  width: 32px;
  height: 32px;
  border: none;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  transition: all 0.2s ease;
  z-index: 10;
}

.close-btn:hover {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.settings-container {
  display: flex;
  height: 100%;
  width: 100%;
  overflow: hidden;
}

.settings-sidebar {
  width: 208px;
  background-color: var(--td-bg-color-settings-modal);
  border-right: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.sidebar-header {
  padding: 16px 14px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.sidebar-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings-nav {
  flex: 1;
  padding: 8px 8px 12px;
  overflow-y: auto;
  min-height: 0;
}

.nav-group-title {
  padding: 6px 14px 2px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 6px 12px;
  margin-bottom: 2px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  font-size: 14px;
  color: var(--td-text-color-primary);
  user-select: none;
}

.nav-item:hover {
  background-color: var(--td-bg-color-container-hover);
  color: var(--td-text-color-primary);
}

.nav-item.active {
  background-color: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
  font-weight: 500;
}

.nav-icon {
  margin-right: 9px;
  font-size: 16px;
  flex-shrink: 0;
  color: inherit;
}

.nav-label {
  flex: 1;
}

.settings-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding: 24px 32px;
}

.section-content .section-header {
  margin-bottom: 16px;
}

.section-title {
  margin: 0 0 6px 0;
  font-size: 20px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.section-desc {
  margin: 0;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
  line-height: 22px;
}

.section-block-header {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.groups-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  margin: -6px 0 0 110px;
}

.yaml-actions {
  margin-top: 8px;
}

.yaml-editor :deep(textarea) {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 12px;
  line-height: 1.5;
}

.preview-controls {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}

.preview-select {
  width: 240px;
}

.preview-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  margin-bottom: 8px;
}

.preview-hint.denied {
  color: var(--td-error-color);
}

.editor-error {
  margin-top: 8px;
  color: var(--td-error-color);
  font-size: 13px;
  white-space: pre-wrap;
}

/* 底部操作条 */
.editor-footer {
  flex-shrink: 0;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 32px;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.footer-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.footer-actions {
  display: flex;
  gap: 8px;
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
