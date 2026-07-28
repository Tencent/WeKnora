<template>
  <div v-if="visible" class="kb-overlay" @click="close">
    <div class="kb-dropdown" @click.stop @wheel.stop :style="dropdownStyle">
      <!-- 搜索 -->
      <div class="kb-search">
        <input
          ref="searchInput"
          v-model="searchQuery"
          type="text"
          :placeholder="$t('knowledgeBase.searchPlaceholder')"
          class="kb-search-input"
          @keydown.down.prevent="moveSelection(1)"
          @keydown.up.prevent="moveSelection(-1)"
          @keydown.enter.prevent="toggleSelection"
          @keydown.esc="close"
        />
      </div>

      <!-- 列表 -->
      <div class="kb-list" ref="kbList" @wheel.stop>
        <div
          v-for="(kb, index) in filteredKnowledgeBases"
          :key="kb.id"
          :class="['kb-item', { selected: isSelected(kb.id), highlighted: highlightedIndex === index }]"
          @click="toggleKb(kb.id)"
          @mouseenter="highlightedIndex = index"
        >
          <div class="kb-item-left">
            <div class="checkbox" :class="{ checked: isSelected(kb.id) }">
              <svg v-if="isSelected(kb.id)" width="12" height="12" viewBox="0 0 12 12" fill="none">
                <path d="M10 3L4.5 8.5L2 6" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </div>
            <div class="kb-icon" :class="{ 'faq': kb.type === 'faq' }">
              <svg v-if="kb.type === 'faq'" width="14" height="14" viewBox="0 0 24 24" fill="none">
                <path d="M12 22C17.5228 22 22 17.5228 22 12C22 6.47715 17.5228 2 12 2C6.47715 2 2 6.47715 2 12C2 17.5228 6.47715 22 12 22Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                <path d="M9 9C9 7.89543 9.89543 7 11 7H13C14.1046 7 15 7.89543 15 9C15 10.1046 14.1046 11 13 11H12V14" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                <circle cx="12" cy="17" r="1" fill="currentColor"/>
              </svg>
              <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none">
                <path d="M22 19C22 19.5304 21.7893 20.0391 21.4142 20.4142C21.0391 20.7893 20.5304 21 20 21H4C3.46957 21 2.96086 20.7893 2.58579 20.4142C2.21071 20.0391 2 19.5304 2 19V5C2 4.46957 2.21071 3.96086 2.58579 3.58579C2.96086 3.21071 3.46957 3 4 3H9L11 6H20C20.5304 6 21.0391 6.21071 21.4142 6.58579C21.7893 6.96086 22 7.46957 22 8V19Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </div>
            <div class="kb-name-wrap">
              <span class="kb-name">{{ kb.name }}</span>
              <span class="kb-docs">({{ kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0) }})</span>
            </div>
          </div>
        </div>

        <div v-if="filteredKnowledgeBases.length === 0" class="kb-empty">
          {{ searchQuery ? $t('knowledgeBase.noMatch') : $t('knowledgeBase.noKnowledge') }}
        </div>
      </div>

      <!-- 底部操作 -->
      <div class="kb-actions">
        <button @click="selectAll" class="kb-btn">{{ $t('common.selectAll') }}</button>
        <button @click="clearAll" class="kb-btn">{{ $t('common.clear') }}</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useSettingsStore } from '@/stores/settings'
import { listKnowledgeBases } from '@/api/knowledge-base'
import { useI18n } from 'vue-i18n'
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom'
import { PHONE_MAX_WIDTH, useResponsiveViewport } from '@/composables/useResponsiveViewport'
import {
  DEFAULT_POPOVER_EDGE_GAP,
  clampPopoverWidth,
  computeAnchoredPopoverLayout,
} from '@/utils/popoverPosition'
import { observeViewportChanges } from '@/utils/viewportChangeObserver'

interface KnowledgeBase {
  id: string
  name: string
  type?: 'document' | 'faq'
  knowledge_count?: number
  chunk_count?: number
  embedding_model_id?: string
  summary_model_id?: string
}

const { t } = useI18n()

const props = defineProps<{
  visible: boolean
  anchorEl?: any | null // 支持 DOM 节点、ref、组件实例
  dropdownWidth?: number
  offsetY?: number
}>()

const emit = defineEmits(['close', 'update:visible'])

const settingsStore = useSettingsStore()
const { isCompact } = useResponsiveViewport()

// 本地状态
const searchQuery = ref('')
const highlightedIndex = ref(0)
const knowledgeBases = ref<KnowledgeBase[]>([])
const searchInput = ref<HTMLInputElement | null>(null)
const kbList = ref<HTMLElement | null>(null)
const dropdownStyle = ref<Record<string, string>>({})

// props 默认
const dropdownWidth = props.dropdownWidth ?? 300
const offsetY = props.offsetY ?? 8

// 过滤：只显示已初始化（有 embedding & summary）的
const filteredKnowledgeBases = computed(() => {
  const valid = knowledgeBases.value.filter(
    k => k.embedding_model_id && k.summary_model_id
  )
  if (!searchQuery.value) return valid
  const q = searchQuery.value.toLowerCase()
  return valid.filter(k => k.name.toLowerCase().includes(q))
})

const selectedKbIds = computed(() => settingsStore.settings.selectedKnowledgeBases || [])

// helper: 从 props.anchorEl 获取真实 DOM 元素（支持多种传入形式）
const resolveAnchorEl = () => {
  const a = props.anchorEl
  if (!a) return null
  // 如果是 Vue ref：取 .value
  if (typeof a === 'object' && 'value' in a) {
    return a.value ?? null
  }
  // 如果是组件实例（可能有 $el）
  if (typeof a === 'object' && '$el' in a) {
    // @ts-ignore
    return a.$el ?? null
  }
  // 直接 DOM 节点或 DOMRect
  return a
}

const isSelected = (id: string) => selectedKbIds.value.includes(id)

const toggleKb = (id: string) => {
  isSelected(id) ? settingsStore.removeKnowledgeBase(id) : settingsStore.addKnowledgeBase(id)
  // Keep the optional picker search separate from the chat input. Once a
  // mobile user taps a result, hide the keyboard so it cannot cover the list.
  if (isCompact.value) searchInput.value?.blur()
}

const toggleSelection = () => {
  const kb = filteredKnowledgeBases.value[highlightedIndex.value]
  if (kb) toggleKb(kb.id)
}

const moveSelection = (dir: number) => {
  const max = filteredKnowledgeBases.value.length
  if (max === 0) return
  highlightedIndex.value = Math.max(0, Math.min(max - 1, highlightedIndex.value + dir))
  nextTick(() => {
    const items = kbList.value?.querySelectorAll('.kb-item')
    items?.[highlightedIndex.value]?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  })
}

const selectAll = () => settingsStore.selectKnowledgeBases(filteredKnowledgeBases.value.map(k => k.id))
const clearAll = () => settingsStore.clearKnowledgeBases()

const close = () => {
  searchInput.value?.blur()
  emit('update:visible', false)
  emit('close')
}

const loadKnowledgeBases = async () => {
  try {
    const res: any = await listKnowledgeBases()
    if (res?.data && Array.isArray(res.data)) knowledgeBases.value = res.data
  } catch (e) {
    console.error(t('knowledgeBase.loadingFailed'), e)
  }
}

// 计算下拉位置：按当前可视视口约束，避免被 Android 键盘或屏幕边缘遮挡
const updateDropdownPosition = () => {
  const anchor = resolveAnchorEl()
  const zoom = getRootZoom()
  const { width: viewportWidth, height: viewportHeight } = cssViewportSize(zoom)
  const edgeGap = DEFAULT_POPOVER_EDGE_GAP
  const preferredWidth = viewportWidth <= PHONE_MAX_WIDTH ? viewportWidth - edgeGap * 2 : dropdownWidth
  const width = clampPopoverWidth(preferredWidth, viewportWidth, edgeGap)

  const applyFallback = () => {
    dropdownStyle.value = {
      position: 'fixed',
      width: `${width}px`,
      left: `${Math.max(edgeGap, Math.round((viewportWidth - width) / 2))}px`,
      top: `${Math.max(edgeGap, Math.round((viewportHeight - 280) / 2))}px`,
      maxHeight: `${Math.max(0, viewportHeight - edgeGap * 2)}px`,
      transform: 'none',
      margin: '0',
      padding: '0',
    }
  }

  if (!anchor) {
    applyFallback()
    return
  }

  let rawRect: { top: number; left: number; right: number; bottom: number; width: number; height: number } | null = null
  try {
    if (typeof anchor.getBoundingClientRect === 'function') {
      const rect = anchor.getBoundingClientRect()
      rawRect = { top: rect.top, left: rect.left, right: rect.right, bottom: rect.bottom, width: rect.width, height: rect.height }
    } else if (anchor.width !== undefined && anchor.left !== undefined) {
      rawRect = anchor as DOMRect
    }
  } catch (error) {
    console.error('[KnowledgeBaseSelector] Error getting bounding rect:', error)
  }

  if (!rawRect || rawRect.width === 0 || rawRect.height === 0) {
    applyFallback()
    return
  }

  const rect = rectToCssPx(rawRect, zoom)
  const layout = computeAnchoredPopoverLayout(rect, {
    width: viewportWidth,
    height: viewportHeight,
  }, {
    preferredWidth,
    preferredHeight: viewportWidth <= PHONE_MAX_WIDTH ? 380 : 280,
    minSpaceBelow: 160,
    maxHeightRatio: 0.62,
    edgeGap,
    offsetY,
  })

  dropdownStyle.value = {
    ...layout.style,
    transform: 'none',
    margin: '0',
    padding: '0',
  }
}
watch(() => props.visible, async (visible, _previous, onCleanup) => {
  let cancelled = false
  let settleFrame: number | null = null
  let stopViewportObserver: (() => void) | null = null

  onCleanup(() => {
    cancelled = true
    stopViewportObserver?.()
    if (settleFrame !== null) cancelAnimationFrame(settleFrame)
  })

  if (!visible) {
    searchQuery.value = ''
    highlightedIndex.value = 0
    return
  }

  await loadKnowledgeBases()
  if (cancelled || !props.visible) return

  await nextTick()
  if (cancelled || !props.visible) return

  updateDropdownPosition()
  settleFrame = requestAnimationFrame(() => {
    settleFrame = null
    if (!cancelled && props.visible) updateDropdownPosition()
  })
  stopViewportObserver = observeViewportChanges(() => {
    if (!cancelled && props.visible) updateDropdownPosition()
  })
  // Do not summon the software keyboard merely by opening the picker.
  if (!isCompact.value) searchInput.value?.focus({ preventScroll: true })
}, { immediate: true })
</script>

<style scoped lang="less">
// 确保所有元素使用 border-box 盒模型
.kb-overlay,
.kb-overlay *,
.kb-overlay *::before,
.kb-overlay *::after {
  box-sizing: border-box;
}

.kb-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  background: transparent;
  /* 遮罩自身不滚动，但允许下拉列表进行纵向触摸滚动 */
  touch-action: manipulation;
  overscroll-behavior: contain;
}

/* 下拉面板使用 fixed 定位，相对于视口 */
.kb-dropdown {
  position: fixed !important;
  background: var(--td-bg-color-container);
  border: .5px solid var(--td-component-border);
  border-radius: 10px;
  box-shadow: var(--td-shadow-2);
  overflow: hidden;
  animation: fadeIn 0.15s ease-out;
  z-index: 10000;
  margin: 0;
  /* 确保定位准确，动画使用 scale 而不是 translate */
  transform-origin: top left;
  display: flex;
  flex-direction: column;
}

/* 宽度由 JS 控制（dropdownWidth），这里只做内部样式 */
.kb-search {
  padding: 8px 10px;
  border-bottom: .5px solid var(--td-component-stroke);
}
.kb-search-input {
  width: 100%;
  padding: 6px 10px;
  font-size: 12px;
  border: .5px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  outline: none;
  transition: border 0.12s;
}
.kb-search-input:focus {
  border-color: var(--td-success-color);
  background: var(--td-bg-color-container);
}

.kb-list {
  flex: 1;
  min-height: 0; /* 允许 flex 子元素缩小 */
  max-height: 260px;
  overflow-y: auto;
  padding: 6px 8px;
  /* 确保滚动限制在此容器内 */
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-y;
}

.kb-item {
  display: flex;
  align-items: center;
  padding: 6px 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.12s;
  margin-bottom: 4px;
}
.kb-item:last-child { margin-bottom: 0; }

.kb-item:hover,
.kb-item.highlighted { background: var(--td-bg-color-secondarycontainer); }

.kb-item.selected { background: var(--td-brand-color-light); }

.kb-item-left {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.checkbox {
  width: 16px; height: 16px;
  border-radius: 3px;
  border: 1.5px solid var(--td-component-border);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.checkbox.checked {
  background: var(--td-success-color);
  border-color: var(--td-success-color);
}
.checkbox.checked svg {
  width: 10px;
  height: 10px;
}
.kb-icon {
  width: 16px;
  height: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: var(--td-brand-color-active);
  
  &.faq {
    color: var(--td-brand-color);
  }
}
.kb-name-wrap { display:flex; flex-direction: row; align-items: center; gap: 4px; min-width: 0; }
.kb-name { font-size: 12px; color: var(--td-text-color-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; line-height: 1.4; }
.kb-docs { font-size: 11px; color: var(--td-text-color-placeholder); flex-shrink: 0; }

.kb-empty { padding: 20px 8px; text-align: center; color: var(--td-text-color-placeholder); font-size: 12px; }

.kb-actions {
  display: flex;
  gap: 8px;
  padding: 8px 10px;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}
.kb-btn {
  flex: 1;
  padding: 6px 10px;
  border-radius: 6px;
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  font-size: 12px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: all 0.12s;
}
.kb-btn:hover {
  border-color: var(--td-success-color);
  color: var(--td-success-color);
  background: var(--td-brand-color-light);
}

@keyframes fadeIn {
  from { opacity: 0; transform: scale(0.98); }
  to { opacity: 1; transform: scale(1); }
}
</style>
