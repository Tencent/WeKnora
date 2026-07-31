<template>
  <div v-if="visible" class="folder-tree-overlay" @click="close">
    <div class="folder-tree-dropdown" @click.stop @wheel.stop :style="dropdownStyle">
      <div class="ft-header">
        <span class="ft-title">{{ $t('knowledgeFolder.selectFolder') }}</span>
        <button v-if="selectedFolderId" class="ft-clear-btn" @click="clearFolder">
          {{ $t('common.clear') }}
        </button>
      </div>

      <!-- Search input -->
      <div class="ft-search">
        <input
          ref="searchInput"
          v-model="searchQuery"
          type="text"
          :placeholder="$t('knowledgeBase.searchPlaceholder')"
          class="ft-search-input"
          @keydown.esc="close"
        />
      </div>

      <!-- Content area -->
      <div class="ft-list" ref="treeList" @wheel.stop>
        <!-- Loading -->
        <div v-if="treeLoading" class="ft-loading">
          <t-loading size="small" />
        </div>

        <!-- Empty state -->
        <div v-else-if="allTreeData.length === 0 && !searchQuery" class="ft-empty">
          {{ $t('knowledgeFolder.noFolders') }}
        </div>

        <!-- Flat filtered list (when searching) -->
        <template v-else-if="searchQuery">
          <div v-if="filteredFolders.length === 0" class="ft-empty">
            {{ $t('common.noResult') }}
          </div>
          <div
            v-for="f in filteredFolders"
            :key="f.id"
            :class="['ft-item', { selected: f.id === selectedFolderId }]"
            @click="selectFolder(f.id, f.kbId, f.label)"
          >
            <t-icon name="folder-open" class="ft-icon-folder" />
            <div class="ft-item-text">
              <span class="ft-name" :title="f.path">{{ f.path }}</span>
              <span class="ft-kb-tag">{{ f.kbName }}</span>
            </div>
          </div>
        </template>

        <!-- Grouped tree by KB (when not searching) -->
        <template v-else>
          <div v-for="group in kbGroups" :key="group.kbId">
            <div class="ft-kb-header">{{ group.kbName }}</div>
            <t-tree
              :data="group.tree"
              :activable="true"
              :actived="actived"
              :hover="true"
              :transition="true"
              :expand-level="1"
              :max-height="240"
              @click="(ctx: any) => selectFolder(ctx.node.value, group.kbId, ctx.node.label)"
            />
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useSettingsStore } from '@/stores/settings'
import { getFolderTree } from '@/api/knowledge-folder'
import { listKnowledgeBases } from '@/api/knowledge-base'
import type { KnowledgeFolder } from '@/types/knowledgeFolder'
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom'

interface TreeNode {
  value: string
  label: string
  children?: TreeNode[]
}

interface FlatFolder {
  id: string
  label: string
  path: string
  kbId: string
  kbName: string
}

interface KBGroup {
  kbId: string
  kbName: string
  tree: TreeNode[]
}

const props = defineProps<{
  visible: boolean
  anchorEl?: any | null
  kbId?: string
  dropdownWidth?: number
  offsetY?: number
}>()

const emit = defineEmits(['close', 'update:visible'])

const settingsStore = useSettingsStore()

const kbGroups = ref<KBGroup[]>([])
const treeLoading = ref(false)
const treeList = ref<HTMLElement | null>(null)
const dropdownStyle = ref<Record<string, string>>({})
const searchQuery = ref('')
const searchInput = ref<HTMLInputElement | null>(null)

const dropdownWidth = props.dropdownWidth ?? 340
const offsetY = props.offsetY ?? 8

const selectedFolderId = computed(() => {
  const folders = settingsStore.settings.selectedFolderIds || []
  return folders.length > 0 ? folders[0].id : undefined
})

// Current activated tree node(s)
const actived = computed(() => {
  const folders = settingsStore.settings.selectedFolderIds || []
  if (folders.length === 0) return []
  return folders[0].id === '__root__' ? [] : [folders[0].id]
})

// Flat all folders across KBs for search filtering
const allTreeData = computed<TreeNode[]>(() => {
  return kbGroups.value.flatMap(g => g.tree)
})

const filteredFolders = computed<FlatFolder[]>(() => {
  if (!searchQuery.value) return []
  const q = searchQuery.value.toLowerCase()
  const result: FlatFolder[] = []
  for (const group of kbGroups.value) {
    const walk = (nodes: TreeNode[], parentPath: string) => {
      for (const n of nodes) {
        const displayPath = parentPath ? parentPath + ' / ' + n.label : n.label
        if (n.label.toLowerCase().includes(q) || displayPath.toLowerCase().includes(q)) {
          result.push({ id: n.value, label: n.label, path: displayPath, kbId: group.kbId, kbName: group.kbName })
        }
        if (n.children?.length) walk(n.children, displayPath)
      }
    }
    walk(group.tree, '')
  }
  return result
})

const buildTreeData = (folders: KnowledgeFolder[]): TreeNode[] => {
  const convert = (nodes: KnowledgeFolder[]): TreeNode[] => {
    return nodes.map(f => ({
      value: f.id,
      label: f.name,
      children: f.children?.length ? convert(f.children) : undefined,
    }))
  }
  return convert(folders)
}

const resolveAnchorEl = () => {
  const a = props.anchorEl
  if (!a) return null
  if (typeof a === 'object' && 'value' in a) return a.value ?? null
  if (typeof a === 'object' && '$el' in a) return (a as any).$el ?? null
  return a
}

const selectFolder = (folderId: string | null, kbId: string, folderName?: string) => {
  if (folderId === null) {
    settingsStore.clearFolders()
    settingsStore.addFolder({ id: '__root__', name: 'Root', kbId: kbId || '', kbName: '' })
  } else {
    settingsStore.clearFolders()
    settingsStore.addFolder({
      id: folderId,
      name: folderName || folderId,
      kbId: kbId || '',
    })
  }
  close()
}

const clearFolder = () => {
  settingsStore.clearFolders()
  close()
}

const close = () => {
  emit('update:visible', false)
  emit('close')
}

const loadFolderTrees = async () => {
  treeLoading.value = true
  kbGroups.value = []
  try {
    // Use specified KB or load all accessible KBs
    let kbIds: string[] = []
    if (props.kbId) {
      kbIds = [props.kbId]
    } else {
      // Load folders from all accessible KBs
      const res: any = await listKnowledgeBases()
      const kbs = Array.isArray(res?.data) ? res.data : (Array.isArray(res) ? res : [])
      kbIds = kbs.filter((kb: any) => kb.embedding_model_id && kb.summary_model_id).map((kb: any) => kb.id)
    }

    const groups: KBGroup[] = []
    for (const id of kbIds) {
      try {
        const res: any = await getFolderTree(id)
        let list: KnowledgeFolder[] = []
        if (Array.isArray(res)) {
          list = res
        } else if (res?.data) {
          list = Array.isArray(res.data) ? res.data : (res.data.children || [])
        } else if (res?.children) {
          list = res.children
        }
        if (list.length > 0) {
          // Get KB name
          const kbRes: any = await listKnowledgeBases()
          const kbs = Array.isArray(kbRes?.data) ? kbRes.data : (Array.isArray(kbRes) ? kbRes : [])
          const kb = kbs.find((k: any) => k.id === id)
          groups.push({
            kbId: id,
            kbName: kb?.name || id,
            tree: buildTreeData(list),
          })
        }
      } catch { /* skip KBs that fail */ }
    }
    kbGroups.value = groups
  } catch (e) {
    console.error('Failed to load folder trees:', e)
  } finally {
    treeLoading.value = false
  }
}

// ---- Positioning ----

const updateDropdownPosition = () => {
  const anchor = resolveAnchorEl()
  const zoom = getRootZoom()
  const { width: vwFallback, height: vhFallback } = cssViewportSize(zoom)

  const applyFallback = () => {
    const topFallback = Math.max(80, vhFallback / 2 - 160)
    dropdownStyle.value = {
      position: 'fixed',
      width: `${dropdownWidth}px`,
      left: `${Math.round((vwFallback - dropdownWidth) / 2)}px`,
      top: `${Math.round(topFallback)}px`,
      maxHeight: '420px',
      transform: 'none', margin: '0', padding: '0',
    }
  }

  if (!anchor) { applyFallback(); return }

  let rawRect: any = null
  try {
    if (typeof anchor.getBoundingClientRect === 'function') {
      rawRect = anchor.getBoundingClientRect()
    } else if (anchor.width !== undefined && anchor.left !== undefined) {
      rawRect = anchor
    }
  } catch { /* fall through */ }

  if (!rawRect || rawRect.width === 0 || rawRect.height === 0) { applyFallback(); return }

  const rect = rectToCssPx(rawRect, zoom)
  const vw = vwFallback
  const vh = vhFallback
  let left = Math.floor(rect.left)
  left = Math.max(16, Math.min(Math.max(16, vw - dropdownWidth - 16), left))

  const preferredHeight = 420
  const minHeight = 220
  const spaceBelow = vh - rect.bottom
  const spaceAbove = rect.top
  let actualHeight: number
  let shouldOpenBelow: boolean

  if (spaceBelow >= minHeight + offsetY) {
    actualHeight = Math.min(preferredHeight, spaceBelow - offsetY - 16)
    shouldOpenBelow = true
  } else {
    const availableHeight = spaceAbove - offsetY - 20
    actualHeight = Math.max(minHeight, Math.min(preferredHeight, availableHeight))
    shouldOpenBelow = false
  }

  if (shouldOpenBelow) {
    dropdownStyle.value = {
      position: 'fixed', width: `${dropdownWidth}px`, left: `${left}px`,
      top: `${Math.floor(rect.bottom + offsetY)}px`, maxHeight: `${actualHeight}px`,
      transform: 'none', margin: '0', padding: '0',
    }
  } else {
    dropdownStyle.value = {
      position: 'fixed', width: `${dropdownWidth}px`, left: `${left}px`,
      bottom: `${vh - rect.top + offsetY}px`, maxHeight: `${actualHeight}px`,
      transform: 'none', margin: '0', padding: '0',
    }
  }
}

let resizeHandler: (() => void) | null = null
let scrollHandler: (() => void) | null = null

watch(() => props.visible, async (v) => {
  if (v) {
    searchQuery.value = ''
    await loadFolderTrees()
    await nextTick()
    searchInput.value?.focus()
    requestAnimationFrame(() => {
      updateDropdownPosition()
      requestAnimationFrame(() => {
        updateDropdownPosition()
        requestAnimationFrame(() => updateDropdownPosition())
      })
    })
    resizeHandler = () => updateDropdownPosition()
    scrollHandler = () => updateDropdownPosition()
    window.addEventListener('resize', resizeHandler, { passive: true })
    window.addEventListener('scroll', scrollHandler, { passive: true, capture: true })
  } else {
    if (resizeHandler) { window.removeEventListener('resize', resizeHandler); resizeHandler = null }
    if (scrollHandler) { window.removeEventListener('scroll', scrollHandler, { capture: true }); scrollHandler = null }
  }
})
</script>

<style scoped lang="less">
.folder-tree-overlay {
  position: fixed; inset: 0; z-index: 9998; background: transparent;
}

.folder-tree-dropdown {
  position: fixed; z-index: 9999;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  box-shadow: var(--td-shadow-2);
  display: flex; flex-direction: column; overflow: hidden;
}

.ft-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 14px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;

  .ft-title { font-size: 13px; font-weight: 600; color: var(--td-text-color-primary); }

  .ft-clear-btn {
    font-size: 12px; color: var(--td-brand-color); border: none;
    background: none; cursor: pointer; padding: 2px 6px;
    &:hover { text-decoration: underline; }
  }
}

.ft-search {
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;

  .ft-search-input {
    width: 100%; padding: 6px 10px;
    border: 1px solid var(--td-component-stroke); border-radius: 6px;
    font-size: 13px; outline: none;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
    transition: border-color 0.15s;
    &::placeholder { color: var(--td-text-color-placeholder); }
    &:focus { border-color: var(--td-brand-color); }
  }
}

.ft-list {
  flex: 1; overflow-y: auto; padding: 4px 8px; min-height: 0;

  :deep(.t-tree) { padding-left: 4px; }

  :deep(.t-tree__item) {
    padding-top: 1px; padding-bottom: 1px; padding-right: 8px; border-radius: 0;
    &:hover { background: var(--td-bg-color-container-hover); }
  }

  :deep(.t-tree__item.t-is-active) {
    background: var(--td-brand-color-light);
    .t-tree__label { color: var(--td-brand-color); font-weight: 500; }
  }

  :deep(.t-tree__label) { font-size: 13px; }
}

.ft-kb-header {
  padding: 8px 14px 4px;
  font-size: 11px; font-weight: 600;
  color: var(--td-text-color-placeholder);
  text-transform: uppercase; letter-spacing: 0.5px;
}

.ft-item {
  display: flex; align-items: center; gap: 8px;
  padding: 7px 14px; cursor: pointer; font-size: 13px;
  transition: background-color 0.15s;

  &:hover { background: var(--td-bg-color-container-hover); }

  &.selected {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color); font-weight: 500;
  }

  .ft-icon-folder { flex-shrink: 0; font-size: 16px; color: var(--td-warning-color); }

  .ft-item-text { flex: 1; min-width: 0; display: flex; align-items: center; gap: 8px; }

  .ft-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .ft-kb-tag {
    flex-shrink: 0; font-size: 10px;
    padding: 1px 6px; border-radius: 4px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }
}

.ft-empty {
  text-align: center; padding: 20px;
  color: var(--td-text-color-placeholder); font-size: 13px;
}

.ft-loading { display: flex; justify-content: center; padding: 20px; }
</style>
