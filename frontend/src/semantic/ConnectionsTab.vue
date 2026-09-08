<template>
  <div class="connections-tab">
    <!-- 卡片网格: 对齐知识库/智能体列表的卡片语言 -->
    <div v-if="modelValue.length" class="card-grid">
      <div v-for="c in modelValue" :key="c.id" class="kb-style-card conn-card" @click="openEdit(c)">
        <div class="card-header">
          <span class="card-title" :title="c.name">
            <span class="card-title-text">{{ c.title || c.name }}</span>
            <span class="card-slug">{{ c.name }}</span>
          </span>
          <t-popup overlay-class-name="card-more-popup" trigger="click" destroy-on-close placement="bottom-right">
            <div class="more-wrap" @click.stop>
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="popup-menu" @click.stop>
                <div class="popup-menu-item" @click.stop="openEdit(c)">
                  <t-icon class="menu-icon" name="setting" />
                  <span>{{ t('semantic.conn.edit') }}</span>
                </div>
                <div v-if="canManage" class="popup-menu-item" @click.stop="testSaved(c)">
                  <t-icon class="menu-icon" name="link" />
                  <span>{{ t('semantic.conn.testSaved') }}</span>
                </div>
                <div v-if="canManage" class="popup-menu-item delete" @click.stop="confirmDelete(c)">
                  <t-icon class="menu-icon" name="delete" />
                  <span>{{ t('semantic.conn.delete') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>

        <div class="card-content">
          <div class="card-description">
            {{ c.description || `${c.type} · ${c.name}` }}
          </div>
        </div>

        <div class="card-bottom">
          <div class="bottom-left">
            <t-tooltip :content="c.guided ? t('semantic.conn.typeGuided') : t('semantic.conn.typePass')" placement="top">
              <div class="feature-badge type-badge" :class="{ guided: c.guided }">
                <t-icon name="server" size="14px" />
                <span class="badge-text">{{ c.type }}</span>
              </div>
            </t-tooltip>
            <div class="feature-badge status-badge" :class="c.status">
              <t-icon :name="c.status === 'active' ? 'check-circle' : 'error-circle'" size="14px" />
              <span class="badge-text">{{ c.status }}</span>
            </div>
          </div>
          <div class="bottom-right">
            <span v-if="c.last_test_result?.ok" class="card-time">
              {{ t('semantic.conn.latency', { ms: c.last_test_result?.latency_ms ?? '-' }) }}
            </span>
            <span class="card-time">{{ shortTime(c.updated_at) }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态: 对齐知识库列表 -->
    <div v-else class="empty-state">
      <img class="empty-img" src="@/assets/img/upload.svg" alt="" />
      <span class="empty-txt">{{ t('semantic.conn.empty') }}</span>
      <t-button v-if="canManage" class="empty-state-btn" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('semantic.conn.add') }}
      </t-button>
    </div>

    <t-dialog
      v-model:visible="dialogVisible"
      :header="editing ? t('semantic.conn.edit') : t('semantic.conn.add')"
      width="640px"
      :confirm-btn="{ content: t('semantic.group.save'), loading: saving }"
      @confirm="save"
    >
      <t-form label-width="120px" label-align="right">
        <t-form-item :label="t('semantic.conn.name')" v-if="!editing">
          <t-input v-model="form.name" :status="nameError ? 'error' : undefined" :placeholder="'erp_main'" />
          <template #help>
            <span class="field-help">{{ nameError || t('semantic.conn.nameHint') }}</span>
          </template>
        </t-form-item>
        <t-form-item :label="t('semantic.conn.title')">
          <t-input v-model="form.title" />
        </t-form-item>
        <t-form-item :label="t('semantic.conn.type')">
          <t-select v-model="form.type" :disabled="!!editing && hasPublishedUsage">
            <t-option-group :label="t('semantic.conn.typeGuided')">
              <t-option v-for="tp in guidedTypes" :key="tp" :value="tp" :label="tp" />
            </t-option-group>
            <t-option-group :label="t('semantic.conn.typePass')">
              <t-option v-for="tp in passthroughTypes" :key="tp" :value="tp" :label="tp" />
            </t-option-group>
          </t-select>
        </t-form-item>
        <template v-if="isGuided">
          <t-form-item :label="t('semantic.conn.host')"><t-input v-model="form.host" /></t-form-item>
          <t-form-item :label="t('semantic.conn.port')"><t-input-number v-model="form.port" :min="1" :max="65535" style="width: 100%" /></t-form-item>
          <t-form-item :label="t('semantic.conn.database')"><t-input v-model="form.database" /></t-form-item>
          <t-form-item :label="t('semantic.conn.username')"><t-input v-model="form.username" /></t-form-item>
          <t-form-item :label="t('semantic.conn.password')">
            <t-input v-model="form.password" type="password" clearable :placeholder="editing ? t('semantic.conn.passwordKeep') : ''" />
          </t-form-item>
        </template>
        <t-form-item :label="t('semantic.conn.extra')">
          <t-textarea v-model="extraText" :autosize="{ minRows: 2, maxRows: 4 }" :placeholder="t('semantic.conn.extraHint')" />
          <template #help>
            <span class="field-help">{{ extraError || t('semantic.conn.extraHint') }}</span>
          </template>
        </t-form-item>
        <t-form-item v-if="isGuided" :label="t('semantic.conn.test')">
          <t-button variant="outline" :loading="testing" @click="testRaw">{{ t('semantic.conn.test') }}</t-button>
          <span v-if="testMessage" :class="testOk ? 'test-ok' : 'test-fail'">{{ testMessage }}</span>
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import {
  CUBE_TYPES_GUIDED,
  CUBE_TYPES_PASSTHROUGH,
  createConnection,
  deleteConnection,
  testConnectionDraft,
  testConnectionRaw,
  testConnectionSaved,
  updateConnection,
  type ConnectionInfo,
  type ConnectionInput
} from './api'

const props = defineProps<{
  modelValue: ConnectionInfo[]
  canManage: boolean
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: ConnectionInfo[]): void; (e: 'changed'): void }>()

const { t } = useI18n()

const slugPattern = /^[a-z][a-z0-9_]{0,62}$/

const guidedTypes = CUBE_TYPES_GUIDED
const passthroughTypes = CUBE_TYPES_PASSTHROUGH.filter(tp => !CUBE_TYPES_GUIDED.includes(tp))

const dialogVisible = ref(false)
const editing = ref<ConnectionInfo | null>(null)
const saving = ref(false)
const testing = ref(false)
const testingId = ref('')
const testMessage = ref('')
const testOk = ref(false)
const extraText = ref('{}')
const form = ref<ConnectionInput & { port?: number }>({})

const hasPublishedUsage = ref(false)

const isGuided = computed(() => !!form.value.type && CUBE_TYPES_GUIDED.includes(form.value.type))
const nameError = computed(() =>
  form.value.name && !slugPattern.test(form.value.name) ? t('semantic.conn.nameHint') : ''
)
const extraError = computed(() => {
  const s = extraText.value.trim()
  if (!s) return ''
  try {
    const v = JSON.parse(s)
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? '' : 'JSON object required'
  } catch {
    return 'invalid JSON'
  }
})

function shortTime(ts: string) {
  return ts ? ts.slice(5, 16) : ''
}

watch(
  () => form.value.type,
  tp => {
    if (tp === 'postgres' && !form.value.port) form.value.port = 5432
    else if (tp === 'clickhouse' && !form.value.port) form.value.port = 8123
    else if (tp === 'sqlserver' && !form.value.port) form.value.port = 1433
    else if (tp === 'mysql' && !form.value.port) form.value.port = 3306
  }
)

function openCreate() {
  editing.value = null
  hasPublishedUsage.value = false
  form.value = { name: '', title: '', type: 'mysql', host: '', database: '', username: '', password: '' }
  extraText.value = ''
  testMessage.value = ''
  dialogVisible.value = true
}

function openEdit(c: ConnectionInfo) {
  editing.value = c
  hasPublishedUsage.value = false
  form.value = {
    name: c.name,
    title: c.title,
    type: c.type,
    host: c.host || '',
    port: c.port || 0,
    database: c.database || '',
    username: c.username || '',
    password: ''
  }
  extraText.value = c.extra && Object.keys(c.extra).length ? JSON.stringify(c.extra, null, 2) : ''
  testMessage.value = ''
  dialogVisible.value = true
}

async function testRaw() {
  testing.value = true
  testMessage.value = ''
  try {
    // 编辑模式: 表单参数 + 存储密码 (密码留空时) 合并校验, 改完未保存也能测
    const resp = editing.value
      ? await testConnectionDraft(editing.value.id, payload())
      : await testConnectionRaw(payload())
    testOk.value = !!resp.test?.ok
    testMessage.value = testOk.value
      ? t('semantic.conn.testOk', { ms: resp.test?.latency_ms ?? '-' })
      : resp.test?.error || t('semantic.conn.testFail')
  } catch (e: any) {
    testOk.value = false
    testMessage.value = e?.response?.data?.error || t('semantic.conn.testFail')
  } finally {
    testing.value = false
  }
}

async function testSaved(c: ConnectionInfo) {
  testingId.value = c.id
  try {
    const resp = await testConnectionSaved(c.id)
    if (resp.test?.ok) {
      c.last_test_result = resp.test
      c.status = 'active'
      MessagePlugin.success(t('semantic.conn.testOk', { ms: resp.test.latency_ms ?? '-' }))
    } else {
      MessagePlugin.warning(resp.test?.error || t('semantic.conn.testFail'))
    }
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || t('semantic.conn.testFail'))
  } finally {
    testingId.value = ''
  }
}

function payload(): ConnectionInput {
  const out: ConnectionInput = { ...form.value }
  const s = extraText.value.trim()
  if (s) out.extra = JSON.parse(s)
  return out
}

async function save() {
  if (!editing.value && (!form.value.name || nameError.value)) {
    MessagePlugin.warning(t('semantic.conn.nameHint'))
    return
  }
  if (extraError.value) {
    MessagePlugin.warning(t('semantic.conn.extraHint'))
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      const updated = await updateConnection(editing.value.id, payload())
      emit(
        'update:modelValue',
        props.modelValue.map(c => (c.id === updated.id ? updated : c))
      )
    } else {
      const created = await createConnection(payload())
      emit('update:modelValue', [...props.modelValue, created])
    }
    MessagePlugin.success(t('semantic.conn.saved'))
    dialogVisible.value = false
    emit('changed')
  } catch (e: any) {
    MessagePlugin.error(e?.response?.data?.error || 'save failed')
  } finally {
    saving.value = false
  }
}

function confirmDelete(c: ConnectionInfo) {
  const dialog = DialogPlugin.confirm({
    header: t('semantic.conn.delete'),
    body: t('semantic.conn.deleteConfirm'),
    confirmBtn: { content: t('semantic.conn.delete'), theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteConnection(c.id)
        emit(
          'update:modelValue',
          props.modelValue.filter(x => x.id !== c.id)
        )
        emit('changed')
      } catch (e: any) {
        MessagePlugin.error(e?.response?.data?.error || t('semantic.conn.deleteBlocked'))
      }
      dialog.destroy()
    },
    onClose: () => dialog.destroy()
  })
}

defineExpose({ openCreate })
</script>

<style scoped>
.field-help {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
.test-ok {
  color: var(--td-success-color);
  margin-left: 8px;
  font-size: 12px;
}
.test-fail {
  color: var(--td-error-color);
  margin-left: 8px;
  font-size: 12px;
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

.feature-badge.type-badge {
  background: rgba(0, 82, 217, 0.08);
  color: var(--td-brand-color);
}

.feature-badge.type-badge.guided {
  background: rgba(7, 192, 95, 0.08);
  color: var(--td-brand-color-active);
}

.feature-badge.status-badge.active {
  background: rgba(7, 192, 95, 0.08);
  color: var(--td-brand-color-active);
}

.feature-badge.status-badge.error {
  background: rgba(227, 77, 89, 0.08);
  color: var(--td-error-color);
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
