<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="batch-overlay" @click.self="handleClose">
        <div class="batch-modal">
          <!-- 顶部标题栏 -->
          <div class="batch-header">
            <h2 class="batch-title">{{ t('batchManage.title') }}</h2>
            <button class="close-btn" @click="handleClose">
              <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
              </svg>
            </button>
          </div>

          <!-- 会话列表 -->
          <div class="batch-body">
            <div class="session-list" v-if="sessions.length > 0">
              <div
                class="session-item"
                v-for="item in sessions"
                :key="item.id"
              >
                <t-checkbox
                  :checked="selectedIds.includes(item.id)"
                  @change="toggleSelect(item.id)"
                />
                <div class="session-info" @click="toggleSelect(item.id)">
                  <div class="session-title">{{ item.title || t('menu.newSession') }}</div>
                  <div class="session-time">{{ formatTime(item.updated_at || item.created_at) }}</div>
                </div>
                <button class="delete-single-btn" @click="handleDeleteSingle(item)">
                  <t-icon name="delete" />
                </button>
              </div>
            </div>
            <div v-else class="empty-state">
              <p>{{ t('menu.newSession') }}</p>
            </div>
          </div>

          <!-- 底部操作栏 -->
          <div class="batch-footer">
            <div class="footer-left">
              <t-checkbox
                :checked="isAllSelected"
                :indeterminate="isIndeterminate"
                @change="toggleSelectAll"
              >
                {{ t('batchManage.selectAll') }}
              </t-checkbox>
            </div>
            <div class="footer-right">
              <t-button theme="default" variant="outline" @click="handleClose">
                {{ t('batchManage.cancel') }}
              </t-button>
              <t-button
                theme="danger"
                :disabled="selectedIds.length === 0"
                :loading="deleting"
                @click="handleBatchDelete"
              >
                {{ t('batchManage.delete') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { batchDelSessions, delSession } from '@/api/chat/index'

const { t } = useI18n()

const props = defineProps<{
  visible: boolean
  sessions: Array<{
    id: string
    title: string
    created_at?: string
    updated_at?: string
  }>
}>()

const emit = defineEmits<{
  (e: 'update:visible', val: boolean): void
  (e: 'deleted', ids: string[]): void
}>()

const selectedIds = ref<string[]>([])
const deleting = ref(false)

const isAllSelected = computed(() =>
  props.sessions.length > 0 && selectedIds.value.length === props.sessions.length
)
const isIndeterminate = computed(() =>
  selectedIds.value.length > 0 && selectedIds.value.length < props.sessions.length
)

watch(() => props.visible, (val) => {
  if (val) selectedIds.value = []
})

const toggleSelect = (id: string) => {
  const idx = selectedIds.value.indexOf(id)
  if (idx > -1) {
    selectedIds.value.splice(idx, 1)
  } else {
    selectedIds.value.push(id)
  }
}

const toggleSelectAll = (checked: boolean) => {
  selectedIds.value = checked ? props.sessions.map(s => s.id) : []
}

const formatTime = (dateStr?: string) => {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const handleDeleteSingle = (item: { id: string; title: string }) => {
  const confirmDialog = DialogPlugin.confirm({
    header: t('batchManage.deleteConfirmTitle'),
    body: t('batchManage.deleteConfirmBody', { count: 1 }),
    confirmBtn: { content: t('common.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      try {
        const res: any = await delSession(item.id)
        if (res && res.success === true) {
          emit('deleted', [item.id])
          MessagePlugin.success(t('batchManage.deleteSuccess'))
        } else {
          MessagePlugin.error(t('batchManage.deleteFailed'))
        }
      } catch {
        MessagePlugin.error(t('batchManage.deleteFailed'))
      }
      confirmDialog.destroy()
    },
  })
}

const handleBatchDelete = () => {
  if (selectedIds.value.length === 0) {
    MessagePlugin.warning(t('batchManage.noSelection'))
    return
  }
  const confirmDialog = DialogPlugin.confirm({
    header: t('batchManage.deleteConfirmTitle'),
    body: t('batchManage.deleteConfirmBody', { count: selectedIds.value.length }),
    confirmBtn: { content: t('common.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      deleting.value = true
      try {
        const ids = [...selectedIds.value]
        const res: any = await batchDelSessions(ids)
        if (res && res.success === true) {
          emit('deleted', ids)
          selectedIds.value = []
          MessagePlugin.success(t('batchManage.deleteSuccess'))
        } else {
          MessagePlugin.error(t('batchManage.deleteFailed'))
        }
      } catch {
        MessagePlugin.error(t('batchManage.deleteFailed'))
      }
      deleting.value = false
      confirmDialog.destroy()
    },
  })
}

const handleClose = () => {
  emit('update:visible', false)
}
</script>

<style lang="less" scoped>
/* 遮罩层 - 与 Settings.vue 保持一致 */
.batch-overlay {
  position: fixed;
  inset: 0;
  z-index: 1100;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
}

/* 弹窗容器 */
.batch-modal {
  position: relative;
  width: 100%;
  max-width: 600px;
  max-height: 70vh;
  background: #ffffff;
  border-radius: 12px;
  box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 顶部标题栏 */
.batch-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 24px 16px;
  border-bottom: 1px solid #e5e7eb;
  flex-shrink: 0;
}

.batch-title {
  font-size: 18px;
  font-weight: 600;
  color: #333333;
  margin: 0;
}

.close-btn {
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: #666666;
  cursor: pointer;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;

  &:hover {
    background: #f5f5f5;
    color: #333333;
  }
}

/* 会话列表区域 */
.batch-body {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

/* 滚动条样式 - 与 Settings 一致 */
.batch-body::-webkit-scrollbar {
  width: 6px;
}
.batch-body::-webkit-scrollbar-track {
  background: #ffffff;
}
.batch-body::-webkit-scrollbar-thumb {
  background: #d0d0d0;
  border-radius: 3px;
}
.batch-body::-webkit-scrollbar-thumb:hover {
  background: #b0b0b0;
}

.session-list {
  padding: 4px 0;
}

.session-item {
  display: flex;
  align-items: center;
  padding: 14px 24px;
  border-bottom: 1px solid #f0f0f0;
  gap: 14px;
  transition: background 0.2s ease;

  &:last-child {
    border-bottom: none;
  }

  &:hover {
    background: #f8f9fa;

    .delete-single-btn {
      opacity: 1;
    }
  }
}

.session-info {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.session-title {
  font-size: 14px;
  font-weight: 400;
  color: #333333;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  line-height: 22px;
}

.session-time {
  font-size: 12px;
  color: #999999;
  margin-top: 4px;
  line-height: 18px;
}

.delete-single-btn {
  opacity: 0;
  transition: all 0.2s ease;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: #999999;
  cursor: pointer;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;

  &:hover {
    color: #e34d59;
    background: rgba(227, 77, 89, 0.06);
  }
}

.empty-state {
  padding: 48px 24px;
  text-align: center;
  color: #999999;
  font-size: 14px;
}

/* 底部操作栏 */
.batch-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 24px;
  border-top: 1px solid #e5e7eb;
  flex-shrink: 0;
  background: #ffffff;
}

.footer-right {
  display: flex;
  gap: 8px;
}

/* 弹窗动画 */
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}

.modal-enter-active .batch-modal,
.modal-leave-active .batch-modal {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .batch-modal,
.modal-leave-to .batch-modal {
  transform: scale(0.95);
  opacity: 0;
}
</style>
