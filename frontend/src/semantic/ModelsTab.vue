<template>
  <div class="models-tab">
    <!-- 卡片网格: 对齐知识库/智能体列表的卡片语言 -->
    <div v-if="visibleModels.length" class="card-grid">
      <div v-for="m in visibleModels" :key="m.id" class="kb-style-card model-card" @click="openEditor(m)">
        <button type="button" class="kb-favorite-star" :class="{ 'is-favorited': isFavorited(m) }"
          @click.stop="toggleFavorite(m, $event)">
          <t-icon :name="isFavorited(m) ? 'star-filled' : 'star'" size="14px" />
        </button>
        <div class="card-header">
          <span class="card-title" :title="m.name">
            <span class="card-title-text">{{ m.title || m.name }}</span>
            <span class="card-slug">{{ m.name }}</span>
          </span>
          <t-popup overlay-class-name="card-more-popup" trigger="click" destroy-on-close placement="bottom-right">
            <div class="more-wrap" @click.stop>
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu" @click.stop>
                <div class="popup-menu-item" @click.stop="openEditor(m)">
                  <t-icon class="menu-icon" name="edit" />
                  <span>{{ t('semantic.model.editor.basic') }}</span>
                </div>
                <div class="popup-menu-item" @click.stop="openVersions(m)">
                  <t-icon class="menu-icon" name="history" />
                  <span>{{ t('semantic.model.versions') }}</span>
                </div>
                <template v-if="canManageModel(m)">
                  <div class="popup-menu-item" @click.stop="publish(m)">
                    <t-icon class="menu-icon" name="cloud-upload" />
                    <span>{{ t('semantic.model.publish') }}</span>
                  </div>
                  <div v-if="m.status === 'published'" class="popup-menu-item" @click.stop="unpublish(m)">
                    <t-icon class="menu-icon" name="cloud-download" />
                    <span>{{ t('semantic.model.unpublish') }}</span>
                  </div>
                  <div class="popup-menu-item delete" @click.stop="confirmDelete(m)">
                    <t-icon class="menu-icon" name="delete" />
                    <span>{{ t('semantic.model.delete') }}</span>
                  </div>
                </template>
              </div>
            </template>
          </t-popup>
        </div>

        <div class="card-content">
          <div class="card-description">
            {{ m.description || t('semantic.model.noDescription') }}
          </div>
        </div>

        <div class="card-bottom">
          <div class="bottom-left">
            <t-tooltip :content="statusLabel(m.status)" placement="top">
              <div class="feature-badge" :class="m.status">
                <t-icon :name="m.status === 'published' ? 'check-circle' : m.status === 'publish_failed' ? 'error-circle' : 'edit-1'" size="14px" />
                <span class="badge-text">{{ statusLabel(m.status) }}</span>
              </div>
            </t-tooltip>
            <div class="feature-badge conn-badge">
              <t-icon name="server" size="14px" />
              <span class="badge-text">{{ connTitle(m.connection_id) }}</span>
            </div>
          </div>
          <div class="bottom-right">
            <span v-if="m.version" class="card-time">v{{ m.version }}</span>
            <span class="card-time">{{ shortTime(m.updated_at) }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态: 对齐知识库列表 -->
    <div v-else class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="" />
      <span class="empty-txt">{{
        spaceSelection === 'favorites' ? t('semantic.model.emptyFavorites')
        : spaceSelection === 'recents' ? t('semantic.model.emptyRecents')
        : t('semantic.model.empty')
      }}</span>
      <t-button v-if="spaceSelection === 'all' && canEdit && connections.length" class="empty-state-btn" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('semantic.model.add') }}
      </t-button>
    </div>

    <!-- 发布说明 -->
    <PublishNoteDialog v-model:visible="noteVisible" @confirm="doPublish" />

    <!-- 新建对话框: 从表生成 / 空白 -->
    <t-dialog v-model:visible="createVisible" :header="t('semantic.model.add')" width="560px" @confirm="create">
      <t-form label-width="110px" label-align="right">
        <t-form-item :label="t('semantic.model.connection')">
          <t-select v-model="createForm.connection_id" @change="onConnChange">
            <t-option v-for="c in connections" :key="c.id" :value="c.id" :label="`${c.title || c.name} (${c.type})`" />
          </t-select>
        </t-form-item>
        <t-form-item :label="t('semantic.model.fromTable')">
          <t-cascader
            v-model="createForm.tableRef"
            :options="tableOptions"
            check-strictly
            clearable
            :placeholder="t('semantic.model.chooseTable')"
            :disabled="!createForm.connection_id"
          />
        </t-form-item>
        <t-form-item :label="t('semantic.model.blank')">
          <t-switch v-model="createForm.blank" />
        </t-form-item>
      </t-form>
      <div class="create-hint">{{ t('semantic.model.publishHint') }}</div>
    </t-dialog>

    <!-- 版本历史 -->
    <t-drawer v-model:visible="versionsVisible" :header="t('semantic.model.versions')" size="480px" :footer="false">
      <t-list v-if="versions.length">
        <t-list-item v-for="v in versions" :key="v.id">
          <div class="version-row">
            <div>
              <div class="version-title">v{{ v.version }}</div>
              <div class="version-note">{{ v.note || '-' }}</div>
              <div class="version-meta">{{ v.published_by }} · {{ v.published_at }}</div>
            </div>
            <t-button v-if="canManageModel(editing!)" size="small" variant="outline" @click="rollback(v)">
              {{ t('semantic.model.rollback') }}
            </t-button>
          </div>
        </t-list-item>
      </t-list>
      <t-empty v-else />
    </t-drawer>

    <ModelEditor
      v-model:visible="editorVisible"
      :model="editing"
      :connections="connections"
      :groups="groups"
      :can-edit="canEdit"
      :can-publish="canPublish"
      :current-user-id="currentUserId"
      @saved="onSaved"
      @published="emit('changed')"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import {
  listTables,
  generateDraft,
  listModels,
  createModel,
  deleteModel,
  publishModel,
  unpublishModel,
  listVersions,
  rollbackModel,
  type ConnectionInfo,
  type DataGroup,
  type ModelVersion,
  type SemanticModel,
  type TableRef
} from './api'
import ModelEditor from './ModelEditor.vue'
import PublishNoteDialog from './PublishNoteDialog.vue'
import { listFavorites, addFavorite, removeFavorite } from '@/api/user-favorites'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{
  connections: ConnectionInfo[]
  groups: DataGroup[]
  spaceSelection: string
  canEdit: boolean
  canPublish: boolean
  currentUserId: string
  cubeReady: boolean
}>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const { t } = useI18n()
const authStore = useAuthStore()

const models = ref<SemanticModel[]>([])
const createVisible = ref(false)
const createForm = ref<{ connection_id: string; tableRef: string[]; blank: boolean }>({
  connection_id: '',
  tableRef: [],
  blank: false
})
const tableOptions = ref<{ label: string; value: string; children?: { label: string; value: string }[] }[]>([])
const generating = ref(false)
const editorVisible = ref(false)
const editing = ref<SemanticModel | null>(null)
const noteVisible = ref(false)
const publishTarget = ref<SemanticModel | null>(null)
const versionsVisible = ref(false)
const versions = ref<ModelVersion[]>([])

// ---- 收藏 (复用平台 user_resource_favorites, type=semantic_model) ----
const favoriteIds = ref<Set<string>>(new Set())

async function loadFavorites() {
  try {
    const resp = await listFavorites('semantic_model')
    favoriteIds.value = new Set((resp.data || []).map(f => f.resource_id))
  } catch {
    favoriteIds.value = new Set()
  }
}
loadFavorites()

function isFavorited(m: SemanticModel) {
  return favoriteIds.value.has(m.id)
}

async function toggleFavorite(m: SemanticModel, ev: Event) {
  ev.stopPropagation()
  try {
    if (isFavorited(m)) {
      await removeFavorite('semantic_model', m.id)
      favoriteIds.value.delete(m.id)
    } else {
      await addFavorite('semantic_model', m.id)
      favoriteIds.value.add(m.id)
    }
    favoriteIds.value = new Set(favoriteIds.value)
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'favorite failed')
  }
}

// 最近查看: 本地记录 (与知识库 recents 同思路, 轻量实现)
const RECENT_KEY = 'semantic_recent_models'
const recentIds = ref<string[]>([])
try {
  recentIds.value = JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
} catch { /* ignore */ }

function markRecent(m: SemanticModel) {
  recentIds.value = [m.id, ...recentIds.value.filter(id => id !== m.id)].slice(0, 10)
  localStorage.setItem(RECENT_KEY, JSON.stringify(recentIds.value))
}

// 侧栏视图过滤 (all | favorites | recents | mine)
const visibleModels = computed(() => {
  if (props.spaceSelection === 'favorites') {
    return models.value.filter(m => favoriteIds.value.has(m.id))
  }
  if (props.spaceSelection === 'mine') {
    const uid = authStore.currentUserId
    return models.value.filter(m => m.created_by === uid)
  }
  if (props.spaceSelection === 'recents') {
    const order = new Map(recentIds.value.map((id, i) => [id, i]))
    return models.value
      .filter(m => order.has(m.id))
      .sort((a, b) => (order.get(a.id) || 0) - (order.get(b.id) || 0))
  }
  return models.value
})

const count = computed(() => models.value.length)
const currentUserId = computed(() => props.currentUserId)

function canManageModel(m: SemanticModel) {
  return m.created_by === currentUserId.value || props.canPublish
}
const favoritesCount = computed(() => models.value.filter(m => favoriteIds.value.has(m.id)).length)
const mineCount = computed(() => models.value.filter(m => m.created_by === authStore.currentUserId).length)
defineExpose({ openCreate, count, favoritesCount, mineCount })

function statusLabel(s: string) {
  if (s === 'published') return t('semantic.model.statusPublished')
  if (s === 'publish_failed') return t('semantic.model.statusFailed')
  return t('semantic.model.statusDraft')
}
function connTitle(id: string) {
  const c = props.connections.find(x => x.id === id)
  return c ? c.title || c.name : '-'
}
function shortTime(ts: string) {
  return ts ? ts.slice(5, 16) : ''
}

async function load() {
  const resp = await listModels()
  models.value = resp.models || []
}
load()

watch(
  () => props.connections,
  () => load(),
  { deep: true }
)

function openCreate() {
  createForm.value = { connection_id: props.connections[0]?.id || '', tableRef: [], blank: false }
  createVisible.value = true
  if (createForm.value.connection_id) onConnChange(createForm.value.connection_id)
}

async function onConnChange(connId: string) {
  if (!connId) return
  generating.value = true
  try {
    const resp = await listTables(connId)
    const bySchema: Record<string, TableRef[]> = {}
    for (const tb of resp.tables || []) {
      ;(bySchema[tb.schema] ||= []).push(tb)
    }
    tableOptions.value = Object.entries(bySchema).map(([schema, tables]) => ({
      label: schema,
      value: schema,
      children: tables.map(tb => ({ label: tb.name, value: tb.name }))
    }))
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'load tables failed')
  } finally {
    generating.value = false
  }
}

async function create() {
  if (!createForm.value.connection_id) {
    MessagePlugin.warning(t('semantic.model.chooseConn'))
    return
  }
  generating.value = true
  try {
    let draftYaml = ''
    if (!createForm.value.blank && createForm.value.tableRef.length >= 1) {
      const [schema, table] =
        createForm.value.tableRef.length === 2
          ? [createForm.value.tableRef[0], createForm.value.tableRef[1]]
          : ['', createForm.value.tableRef[0]]
      const resp = await generateDraft(createForm.value.connection_id, schema, table)
      draftYaml = resp.yaml
    } else {
      draftYaml = `cubes:
  - name: new_cube
    title: ''
    sql: SELECT * FROM schema.table
    data_source: ${props.connections.find(c => c.id === createForm.value.connection_id)?.name || ''}
    measures:
      - name: count
        type: count
    dimensions: []
`
    }
    const created = await createModel({
      connection_id: createForm.value.connection_id,
      kind: 'cube',
      draft_yaml: draftYaml,
      allowed_groups: []
    })
    models.value.push(created)
    createVisible.value = false
    editing.value = created
    editorVisible.value = true
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'create failed')
  } finally {
    generating.value = false
  }
}

function openEditor(m: SemanticModel) {
  markRecent(m)
  editing.value = m
  editorVisible.value = true
}

function openVersions(m: SemanticModel) {
  editing.value = m
  listVersions(m.id).then(resp => {
    versions.value = resp.versions || []
    versionsVisible.value = true
  })
}

function publish(m: SemanticModel) {
  publishTarget.value = m
  noteVisible.value = true
}

async function doPublish(note: string) {
  if (!publishTarget.value) return
  noteVisible.value = false
  try {
    const resp = await publishModel(publishTarget.value.id, note)
    MessagePlugin.success(t('semantic.model.publishOk', { n: resp.version }))
    await load()
  } catch (e: any) {
    MessagePlugin.error(t('semantic.model.publishFail', { err: e?.response?.data?.error || '' }))
    await load()
  }
}

async function unpublish(m: SemanticModel) {
  try {
    await unpublishModel(m.id)
    await load()
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'unpublish failed')
  }
}

function confirmDelete(m: SemanticModel) {
  const dialog = DialogPlugin.confirm({
    header: t('semantic.model.delete'),
    body: t('semantic.model.deleteConfirm'),
    confirmBtn: { content: t('semantic.model.delete'), theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteModel(m.id)
        models.value = models.value.filter(x => x.id !== m.id)
        emit('changed')
      } catch (e: any) {
        MessagePlugin.error(e?.response?.data?.error || 'delete failed')
      }
      dialog.destroy()
    },
    onClose: () => dialog.destroy()
  })
}

async function rollback(v: ModelVersion) {
  if (!editing.value) return
  try {
    await rollbackModel(editing.value.id, v.id)
    MessagePlugin.success(t('semantic.model.publishOk', { n: v.version }))
    versionsVisible.value = false
    await load()
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'rollback failed')
  }
}

function onSaved(updated: SemanticModel) {
  models.value = models.value.map(m => (m.id === updated.id ? updated : m))
}
</script>

<style scoped>
.create-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
.version-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}
.version-title {
  font-weight: 600;
}
.version-note,
.version-meta {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

/* ---- 卡片网格与卡片样式: 复用知识库列表的卡片语言 ---- */
.card-grid {
  display: grid;
  gap: 12px;
  grid-template-columns: 1fr;
}

@media (min-width: 900px) {
  .card-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

.kb-style-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: all 0.25s ease;
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  height: 136px;
  min-height: 136px;
}

.kb-style-card:hover {
  border-color: var(--td-brand-color);
  box-shadow: 0 4px 12px rgba(7, 192, 95, 0.12);
}

.kb-style-card::after {
  content: '';
  position: absolute;
  top: 0;
  right: 0;
  width: 60px;
  height: 60px;
  background: linear-gradient(135deg, rgba(7, 192, 95, 0.08) 0%, transparent 100%);
  border-radius: 0 12px 0 100%;
  pointer-events: none;
  z-index: 0;
}

.card-header,
.card-content,
.card-bottom {
  position: relative;
  z-index: 1;
}

@media (min-width: 1250px) {
  .card-grid {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (min-width: 1600px) {
  .card-grid {
    grid-template-columns: repeat(4, 1fr);
  }
}


.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}

.card-title {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  overflow: hidden;
}

.card-title-text {
  font-size: 16px;
  font-weight: 600;
  line-height: 22px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card-slug {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-family: 'SFMono-Regular', Consolas, Menlo, monospace;
}

.more-wrap {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  flex-shrink: 0;
}

.more-wrap:hover {
  background: var(--td-bg-color-secondarycontainer);
}

.more-wrap .more-icon {
  width: 16px;
  height: 16px;
}

.card-content {
  flex: 1;
  min-height: 0;
  margin-bottom: 8px;
  overflow: hidden;
}

.card-description {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.card-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: 8px;
  border-top: 0.5px solid var(--td-component-stroke);
}

.bottom-left {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.bottom-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.card-time {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.feature-badge {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  height: 22px;
  border-radius: 5px;
  padding: 0 6px;
  cursor: default;
  transition: background 0.2s ease;
  font-size: 11px;
  font-weight: 500;
}

.feature-badge.published {
  background: rgba(7, 192, 95, 0.08);
  color: var(--td-brand-color-active);
}

.feature-badge.draft {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.feature-badge.publish_failed {
  background: rgba(227, 77, 89, 0.08);
  color: var(--td-error-color);
}

.feature-badge.conn-badge {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.kb-favorite-star {
  position: absolute;
  top: 0;
  right: 0;
  z-index: 3;
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background 0.15s ease, color 0.15s ease;
}

.kb-favorite-star:hover {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-warning-color, #e37318);
}

.kb-favorite-star.is-favorited {
  opacity: 1;
  color: var(--td-warning-color, #e37318);
}

.kb-style-card:hover .kb-favorite-star {
  opacity: 1;
}

/* ---- 空状态: 对齐知识库列表 ---- */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 64px 0;
  color: var(--td-text-color-secondary);
}

.empty-state .empty-img {
  width: 120px;
  opacity: 0.8;
}

.empty-state .empty-txt {
  font-size: 15px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.empty-state-btn {
  margin-top: 8px;
  background: linear-gradient(135deg, var(--td-brand-color) 0%, #00a67e 100%);
  border: none;
  color: var(--td-text-color-anti);
}

.empty-state-btn:hover {
  background: linear-gradient(135deg, var(--td-brand-color) 0%, var(--td-brand-color-active) 100%);
}
</style>
