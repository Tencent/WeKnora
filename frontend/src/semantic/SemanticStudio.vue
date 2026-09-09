<template>
  <div class="semantic-studio-container">
    <ListSpaceSidebar
      v-if="!authStore.isLiteMode"
      v-model="spaceSelection"
      :count-all="modelCount"
      :count-favorites="favoriteCount"
      :count-recents="recentCount"
      :count-mine="mineCount"
      show-favorites
      show-recents
    />
    <div class="semantic-studio">
      <!-- 页头: 对齐知识库列表页 -->
      <div class="header">
        <div class="header-title">
          <div class="title-row">
            <h2>{{ t('semantic.menu') }}</h2>
            <t-tag v-if="info?.cube_ready" theme="success" variant="light-outline" size="small">
              {{ t('semantic.info.cubeReady') }}
            </t-tag>
            <t-tooltip v-else :content="info?.cube_error || t('semantic.info.cubeDown')">
              <t-tag theme="danger" variant="light-outline" size="small">
                {{ t('semantic.info.cubeDown') }}
              </t-tag>
            </t-tooltip>
          </div>
          <p class="header-subtitle">{{ t('semantic.info.subtitle') }}</p>
        </div>
        <t-button v-if="isAdmin" variant="text" theme="default" size="small" class="audit-btn"
          @click="openAudit">
          <template #icon><t-icon name="history" size="15px" /></template>
          {{ t('semantic.audit.title') }}
        </t-button>
      </div>

      <t-alert v-if="connectionsLoaded && !connections.length" theme="info" class="conn-hint">
        {{ t('semantic.info.noConnection') }}
      </t-alert>

      <!-- 模型分组 (侧栏视图作用于这里) -->
      <div class="section-block">
        <div class="kb-section-header" @click="toggleSection('models')">
          <t-icon name="layers" size="16px" />
          <span class="kb-section-title">{{ t('semantic.tabs.models') }}</span>
          <span class="kb-section-count">{{ modelCount }}</span>
          <t-tooltip v-if="canEdit && spaceSelection === 'all'" :content="t('semantic.model.add')" placement="top">
            <t-button variant="text" theme="default" size="small" class="section-add-btn"
              :disabled="!connections.length" @click.stop="modelsTabRef?.openCreate()">
              <template #icon><t-icon name="add" size="15px" /></template>
            </t-button>
          </t-tooltip>
          <t-icon class="kb-section-toggle" :name="sections.models ? 'chevron-down' : 'chevron-right'" size="16px" />
        </div>
        <div v-show="sections.models" class="section-body">
          <ModelsTab
            ref="modelsTabRef"
            :connections="connections"
            :groups="groups"
            :space-selection="spaceSelection"
            :can-edit="canEdit"
            :can-publish="isAdmin"
            :current-user-id="authStore.currentUserId"
            :cube-ready="!!info?.cube_ready"
          />
        </div>
      </div>

      <!-- 数据源分组 (仅「全部」视图; 收藏/最近/本空间只看模型) -->
      <div v-if="spaceSelection === 'all'" class="section-block">
        <div class="kb-section-header" @click="toggleSection('connections')">
          <t-icon name="server" size="16px" />
          <span class="kb-section-title">{{ t('semantic.tabs.connections') }}</span>
          <span class="kb-section-count">{{ connections.length }}</span>
          <t-tooltip v-if="isAdmin" :content="t('semantic.conn.add')" placement="top">
            <t-button variant="text" theme="default" size="small" class="section-add-btn"
              @click.stop="connectionsTabRef?.openCreate()">
              <template #icon><t-icon name="add" size="15px" /></template>
            </t-button>
          </t-tooltip>
          <t-icon class="kb-section-toggle" :name="sections.connections ? 'chevron-down' : 'chevron-right'" size="16px" />
        </div>
        <div v-show="sections.connections" class="section-body">
          <ConnectionsTab ref="connectionsTabRef" v-model="connections" :can-manage="isAdmin" @changed="reloadGroups" />
        </div>
      </div>

      <!-- 审计日志抽屉 -->
      <t-drawer v-model:visible="auditVisible" :header="t('semantic.audit.title')" size="560px" :footer="false">
        <t-table row-key="id" :data="auditLogs" :columns="auditColumns" size="small" max-height="70vh">
          <template #action="{ row }"><code class="audit-action">{{ row.action }}</code></template>
          <template #time="{ row }"><span class="audit-time">{{ row.created_at }}</span></template>
        </t-table>
      </t-drawer>

      <!-- 数据组分组 (仅「全部」视图) -->
      <div v-if="spaceSelection === 'all'" class="section-block">
        <div class="kb-section-header" @click="toggleSection('groups')">
          <t-icon name="usergroup" size="16px" />
          <span class="kb-section-title">{{ t('semantic.tabs.groups') }}</span>
          <span class="kb-section-count">{{ groups.length }}</span>
          <t-tooltip v-if="isAdmin" :content="t('semantic.group.add')" placement="top">
            <t-button variant="text" theme="default" size="small" class="section-add-btn"
              @click.stop="groupsTabRef?.openCreate()">
              <template #icon><t-icon name="add" size="15px" /></template>
            </t-button>
          </t-tooltip>
          <t-icon class="kb-section-toggle" :name="sections.groups ? 'chevron-down' : 'chevron-right'" size="16px" />
        </div>
        <div v-show="sections.groups" class="section-body">
          <GroupsTab ref="groupsTabRef" v-model="groups" :can-manage="isAdmin" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import {
  getModuleInfo, listConnections, listGroups, listAudits,
  type AuditLogEntry, type ConnectionInfo, type DataGroup, type ModuleInfo
} from './api'
import ListSpaceSidebar from '@/components/ListSpaceSidebar.vue'
import ModelsTab from './ModelsTab.vue'
import ConnectionsTab from './ConnectionsTab.vue'
import GroupsTab from './GroupsTab.vue'

const { t } = useI18n()
const authStore = useAuthStore()

// 分组折叠状态 (对齐知识库的分组折叠交互)
const sections = ref({ models: true, connections: true, groups: false })

function toggleSection(key: 'models' | 'connections' | 'groups') {
  sections.value[key] = !sections.value[key]
}

const info = ref<ModuleInfo | null>(null)
const connections = ref<ConnectionInfo[]>([])
const connectionsLoaded = ref(false)
const groups = ref<DataGroup[]>([])

// 侧栏视图 (作用于模型分组)
const spaceSelection = ref('all')

const modelsTabRef = ref<InstanceType<typeof ModelsTab> | null>(null)
const connectionsTabRef = ref<InstanceType<typeof ConnectionsTab> | null>(null)
const groupsTabRef = ref<InstanceType<typeof GroupsTab> | null>(null)

const isAdmin = computed(() => authStore.hasRole('admin'))
const canEdit = computed(() => authStore.hasRole('contributor'))

// 审计日志 (admin)
const auditVisible = ref(false)
const auditLogs = ref<AuditLogEntry[]>([])

const auditColumns = computed(() => [
  { colKey: 'action', title: t('semantic.audit.action'), width: 170 },
  { colKey: 'target', title: t('semantic.audit.target'), minWidth: 140 },
  { colKey: 'user_id', title: t('semantic.audit.user'), width: 100, ellipsis: true },
  { colKey: 'time', title: t('semantic.audit.time'), width: 160 }
])

function openAudit() {
  auditVisible.value = true
  listAudits().then(resp => { auditLogs.value = resp.audit_logs || [] }).catch(() => { auditLogs.value = [] })
}

const modelCount = computed(() => modelsTabRef.value?.count ?? 0)
const favoriteCount = computed(() => modelsTabRef.value?.favoritesCount ?? 0)
const mineCount = computed(() => modelsTabRef.value?.mineCount ?? 0)
const recentCount = computed(() => Math.min(modelCount.value, 10))

async function loadConnections() {
  try {
    const resp = await listConnections()
    connections.value = resp.connections || []
  } finally {
    connectionsLoaded.value = true
  }
}

async function reloadGroups() {
  try {
    const resp = await listGroups()
    groups.value = resp.groups || []
  } catch {
    groups.value = []
  }
}

onMounted(async () => {
  try {
    info.value = await getModuleInfo()
  } catch {
    info.value = null
  }
  loadConnections()
  reloadGroups()
})
</script>

<style scoped>
.semantic-studio-container {
  height: 100%;
  display: flex;
  min-width: 0;
  min-height: 0;
  box-sizing: border-box;
}

.semantic-studio {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 20px 28px 0 20px;
  overflow-y: auto;
  overflow-x: hidden;
}

.header {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 4px;
}

.audit-btn {
  margin-left: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  color: var(--td-text-color-secondary);
}

.audit-action {
  font-size: 12px;
  font-family: 'SFMono-Regular', Consolas, Menlo, monospace;
  color: var(--td-text-color-secondary);
}

.audit-time {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.header-title {
  display: flex;
  flex-direction: column;
}

.title-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.header-title h2 {
  margin: 0;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
}

.header-subtitle {
  margin: 4px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.conn-hint {
  margin: 12px 0 4px;
}

/* ---- 分组: 照抄知识库 kb-section-header ---- */
.section-block {
  margin-top: 16px;
}

.kb-section-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 4px 6px 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  font-weight: 600;
  line-height: 20px;
  cursor: pointer;
  user-select: none;
}

.kb-section-header:hover {
  color: var(--td-text-color-primary);
}

.kb-section-title {
  font-family: var(--app-font-family);
}

.kb-section-count {
  margin-left: 2px;
  padding: 0 6px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 16px;
  font-weight: 500;
}

.kb-section-toggle {
  margin-left: 4px;
  opacity: 0.7;
}

.kb-section-header:hover .kb-section-toggle {
  opacity: 1;
}

.section-add-btn {
  margin-left: auto;
  padding: 0 !important;
  min-width: 28px !important;
  width: 28px !important;
  height: 28px !important;
  display: inline-flex !important;
  align-items: center !important;
  justify-content: center !important;
  background: var(--td-bg-color-secondarycontainer) !important;
  border: 1px solid var(--td-component-stroke) !important;
  border-radius: 6px !important;
  color: var(--td-text-color-secondary);
}

.section-body {
  padding: 10px 0 4px;
}
</style>
