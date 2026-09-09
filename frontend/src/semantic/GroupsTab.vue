<template>
  <div class="groups-tab">
    <!-- 卡片网格: 对齐知识库/智能体列表的卡片语言 -->
    <div v-if="modelValue.length" class="card-grid">
      <div v-for="g in modelValue" :key="g.id" class="kb-style-card group-card" @click="canManage && openEdit(g)">
        <div class="card-header">
          <span class="card-title" :title="g.name">
            <span class="card-title-text">{{ g.title || g.name }}</span>
            <span class="card-slug">{{ g.name }}</span>
          </span>
          <t-popup overlay-class-name="card-more-popup" trigger="click" destroy-on-close placement="bottom-right">
            <div class="more-wrap" @click.stop>
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu" @click.stop>
                <div v-if="canManage" class="popup-menu-item" @click.stop="openMembers(g)">
                  <t-icon class="menu-icon" name="usergroup" />
                  <span>{{ t('semantic.group.members') }}</span>
                </div>
                <div v-if="canManage" class="popup-menu-item" @click.stop="openEdit(g)">
                  <t-icon class="menu-icon" name="edit" />
                  <span>{{ t('semantic.group.edit') }}</span>
                </div>
                <div v-if="canManage" class="popup-menu-item delete" @click.stop="confirmDelete(g)">
                  <t-icon class="menu-icon" name="delete" />
                  <span>{{ t('semantic.group.delete') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>

        <div class="card-content">
          <div class="card-description">
            {{ g.description || t('semantic.group.noDescription') }}
          </div>
        </div>

        <div class="card-bottom">
          <div class="bottom-left">
            <div class="feature-badge member-badge">
              <t-icon name="usergroup" size="14px" />
              <span class="badge-text">{{ t('semantic.group.memberCount', { n: memberCounts[g.id] ?? 0 }) }}</span>
            </div>
          </div>
          <div class="bottom-right">
            <span class="card-time">{{ shortTime(g.updated_at) }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态: 对齐知识库列表 -->
    <div v-else class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="" />
      <span class="empty-txt">{{ t('semantic.group.empty') }}</span>
      <t-button v-if="canManage" class="empty-state-btn" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('semantic.group.add') }}
      </t-button>
    </div>

    <t-dialog
      v-model:visible="dialogVisible"
      :header="editing ? t('semantic.group.edit') : t('semantic.group.add')"
      width="520px"
      :confirm-btn="{ content: t('semantic.group.save'), loading: saving }"
      @confirm="save"
    >
      <t-form label-width="110px" label-align="right">
        <t-form-item :label="t('semantic.group.name')" v-if="!editing">
          <t-input v-model="form.name" :status="nameError ? 'error' : undefined" placeholder="sales" />
          <template #help><span class="field-help">{{ nameError || t('semantic.group.nameHint') }}</span></template>
        </t-form-item>
        <t-form-item :label="t('semantic.group.title')"><t-input v-model="form.title" /></t-form-item>
        <t-form-item :label="t('semantic.group.description')"><t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }" /></t-form-item>
      </t-form>
    </t-dialog>

    <!-- 成员管理 -->
    <t-drawer v-model:visible="membersVisible" :header="`${t('semantic.group.members')} · ${memberGroup?.title || memberGroup?.name || ''}`" size="560px">
      <div class="member-hint">{{ t('semantic.group.memberHint') }}</div>
      <t-input v-model="memberSearch" clearable :placeholder="t('semantic.group.searchUser')" class="member-search">
        <template #suffix-icon><search-icon /></template>
      </t-input>
      <t-checkbox-group v-model="checkedUserIds" class="member-list">
        <div v-for="m in filteredMembers" :key="m.user_id" class="member-row">
          <t-checkbox :value="m.user_id" :label="`${m.username || m.email} (${m.email})`" />
        </div>
        <t-empty v-if="!filteredMembers.length" size="small" :description="t('semantic.group.noMembers')" />
      </t-checkbox-group>
      <t-button variant="text" size="small" theme="primary" class="goto-members" @click="gotoMembers">
        {{ t('semantic.group.gotoMembers') }} →
      </t-button>
      <template #footer>
        <t-button theme="primary" :loading="savingMembers" @click="saveMembers">{{ t('semantic.group.save') }}</t-button>
      </template>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { SearchIcon } from 'tdesign-icons-vue-next'
import { createGroup, deleteGroup, getGroupUsage, listGroupMembers, setGroupMembers, updateGroup, type DataGroup } from './api'
import { fetchAllTenantMembers, type TenantMember } from '@/api/tenant/members'
import { useAuthStore } from '@/stores/auth'
import { useRouter } from 'vue-router'

const props = defineProps<{
  modelValue: DataGroup[]
  canManage: boolean
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: DataGroup[]): void }>()

const { t } = useI18n()
const router = useRouter()
const slugPattern = /^[a-z][a-z0-9_]{0,62}$/

const dialogVisible = ref(false)
const editing = ref<DataGroup | null>(null)
const saving = ref(false)
const form = ref<{ name: string; title: string; description: string }>({ name: '', title: '', description: '' })
const membersVisible = ref(false)
const memberGroup = ref<DataGroup | null>(null)
const members = ref<TenantMember[]>([])
const memberSearch = ref('')
const checkedUserIds = ref<string[]>([])
const savingMembers = ref(false)
const memberCounts = ref<Record<string, number>>({})

const nameError = computed(() =>
  form.value.name && !slugPattern.test(form.value.name) ? t('semantic.group.nameHint') : ''
)
const filteredMembers = computed(() => {
  const q = memberSearch.value.trim().toLowerCase()
  if (!q) return members.value
  return members.value.filter(
    m => m.email?.toLowerCase().includes(q) || m.username?.toLowerCase().includes(q)
  )
})

function shortTime(ts?: string) {
  return ts ? ts.slice(5, 16) : ''
}

async function loadMemberCounts() {
  for (const g of props.modelValue) {
    try {
      const resp = await listGroupMembers(g.id)
      memberCounts.value[g.id] = resp.user_ids?.length ?? 0
    } catch {
      memberCounts.value[g.id] = 0
    }
  }
}
loadMemberCounts()

function openCreate() {
  editing.value = null
  form.value = { name: '', title: '', description: '' }
  dialogVisible.value = true
}

function openEdit(g: DataGroup) {
  editing.value = g
  form.value = { name: g.name, title: g.title, description: g.description }
  dialogVisible.value = true
}

async function save() {
  if (!editing.value && (!form.value.name || nameError.value)) {
    MessagePlugin.warning(t('semantic.group.nameHint'))
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      const updated = await updateGroup(editing.value.id, form.value)
      emit(
        'update:modelValue',
        props.modelValue.map(g => (g.id === updated.id ? updated : g))
      )
    } else {
      const created = await createGroup(form.value)
      emit('update:modelValue', [...props.modelValue, created])
      memberCounts.value[created.id] = 0
    }
    dialogVisible.value = false
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'save failed')
  } finally {
    saving.value = false
  }
}

async function confirmDelete(g: DataGroup) {
  // 删除前查引用: 被模型引用的组删除后模型需重新发布
  let warn = ''
  try {
    const usage = await getGroupUsage(g.id)
    if (usage.model_count > 0) {
      warn = ' ' + t('semantic.group.usageWarn', {
        n: usage.model_count,
        names: usage.model_names.join(', ')
      })
    }
  } catch { /* usage check is best-effort */ }
  const dialog = DialogPlugin.confirm({
    header: t('semantic.group.delete'),
    body: t('semantic.group.deleteConfirm') + warn,
    confirmBtn: { content: t('semantic.group.delete'), theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteGroup(g.id)
        emit(
          'update:modelValue',
          props.modelValue.filter(x => x.id !== g.id)
        )
      } catch (e: any) {
        MessagePlugin.error(e?.response?.data?.error || 'delete failed')
      }
      dialog.destroy()
    },
    onClose: () => dialog.destroy()
  })
}

async function openMembers(g: DataGroup) {
  memberGroup.value = g
  memberSearch.value = ''
  membersVisible.value = true
  const tenantId = useAuthStore().currentTenantId
  const [allMembers, groupMembersResp] = await Promise.all([
    fetchAllTenantMembers(tenantId as unknown as number),
    listGroupMembers(g.id)
  ])
  members.value = allMembers
  checkedUserIds.value = groupMembersResp.user_ids || []
}

function gotoMembers() {
  router.push('/platform/settings?section=members')
}

async function saveMembers() {
  if (!memberGroup.value) return
  savingMembers.value = true
  try {
    await setGroupMembers(memberGroup.value.id, checkedUserIds.value)
    memberCounts.value[memberGroup.value.id] = checkedUserIds.value.length
    MessagePlugin.success(t('semantic.group.save'))
    membersVisible.value = false
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'save failed')
  } finally {
    savingMembers.value = false
  }
}

defineExpose({ openCreate })
</script>

<style scoped>
.field-help {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
.member-hint {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin-bottom: 8px;
}
.member-search {
  margin-bottom: 8px;
}
.member-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 55vh;
  overflow: auto;
}
.member-row {
  padding: 2px 0;
}
.goto-members {
  margin-top: 4px;
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

.feature-badge.member-badge {
  background: rgba(124, 77, 255, 0.08);
  color: var(--td-brand-color);
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
