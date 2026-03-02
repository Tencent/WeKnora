<template>
  <div
    ref="sidebarRef"
    class="list-space-sidebar"
    :class="{ expanded: isExpanded, dragging: isDragging }"
    :style="{ width: isDragging ? `${dragWidth}px` : undefined }"
  >
    <!-- Collapsed: icon strip -->
    <div v-if="!isExpanded" class="icon-strip">
      <template v-if="mode === 'resource'">
        <t-tooltip v-if="!hideAll" :content="tooltipText($t('listSpaceSidebar.all'), countAll)" placement="right" :show-arrow="false">
          <div class="icon-item-labeled" :class="{ active: selected === 'all' }" @click="select('all')">
            <t-icon name="layers" size="16px" />
            <span class="icon-label">{{ $t('listSpaceSidebar.all') }}</span>
          </div>
        </t-tooltip>
        <t-tooltip :content="tooltipText($t('listSpaceSidebar.mine'), countMine)" placement="right" :show-arrow="false">
          <div class="icon-item-labeled" :class="{ active: selected === 'mine' }" @click="select('mine')">
            <t-icon name="user" size="16px" />
            <span class="icon-label">{{ $t('listSpaceSidebar.mine') }}</span>
          </div>
        </t-tooltip>
        <t-tooltip v-if="!hideShared" :content="tooltipText($t('listSpaceSidebar.sharedToMe'), countShared)" placement="right" :show-arrow="false">
          <div class="icon-item-labeled" :class="{ active: selected === 'shared' }" @click="select('shared')">
            <t-icon name="share" size="16px" />
            <span class="icon-label">{{ $t('listSpaceSidebar.sharedToMe') }}</span>
          </div>
        </t-tooltip>
        <template v-if="organizationsWithCount.length">
          <div class="icon-strip-divider" />
          <t-tooltip v-for="org in organizationsWithCount" :key="org.id" :content="tooltipText(org.name, getOrgCount(org.id))" placement="right" :show-arrow="false">
            <div class="icon-item-labeled" :class="{ active: selected === org.id }" @click="select(org.id)">
              <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
              <span class="icon-label">{{ truncateLabel(org.name) }}</span>
            </div>
          </t-tooltip>
        </template>
      </template>

      <template v-else>
        <t-tooltip :content="tooltipText($t('listSpaceSidebar.all'), countAll)" placement="right" :show-arrow="false">
          <div class="icon-item-labeled" :class="{ active: selected === 'all' }" @click="select('all')">
            <t-icon name="layers" size="16px" />
            <span class="icon-label">{{ $t('listSpaceSidebar.all') }}</span>
          </div>
        </t-tooltip>
        <t-tooltip :content="tooltipText($t('organization.createdByMe'), countCreated)" placement="right" :show-arrow="false">
          <div class="icon-item-labeled" :class="{ active: selected === 'created' }" @click="select('created')">
            <t-icon name="usergroup-add" size="16px" />
            <span class="icon-label">{{ $t('organization.createdByMe') }}</span>
          </div>
        </t-tooltip>
        <t-tooltip :content="tooltipText($t('organization.joinedByMe'), countJoined)" placement="right" :show-arrow="false">
          <div class="icon-item-labeled" :class="{ active: selected === 'joined' }" @click="select('joined')">
            <t-icon name="usergroup" size="16px" />
            <span class="icon-label">{{ $t('organization.joinedByMe') }}</span>
          </div>
        </t-tooltip>
      </template>
    </div>

    <!-- Expanded: full nav panel -->
    <nav v-else class="expanded-panel">
      <div
        v-if="!hideAll"
        class="sidebar-item"
        :class="{ active: selected === 'all' }"
        @click="select('all')"
      >
        <div class="item-left">
          <t-icon name="layers" class="item-icon" />
          <span class="item-label">{{ $t('listSpaceSidebar.all') }}</span>
        </div>
        <span v-if="countAll !== undefined" class="item-count">{{ countAll }}</span>
      </div>

      <template v-if="mode === 'resource'">
        <div
          class="sidebar-item"
          :class="{ active: selected === 'mine' }"
          @click="select('mine')"
        >
          <div class="item-left">
            <t-icon name="user" class="item-icon" />
            <span class="item-label">{{ $t('listSpaceSidebar.mine') }}</span>
          </div>
          <span v-if="countMine !== undefined" class="item-count">{{ countMine }}</span>
        </div>
        <div
          v-if="!hideShared"
          class="sidebar-item"
          :class="{ active: selected === 'shared' }"
          @click="select('shared')"
        >
          <div class="item-left">
            <t-icon name="share" class="item-icon" />
            <span class="item-label">{{ $t('listSpaceSidebar.sharedToMe') }}</span>
          </div>
          <span v-if="countShared !== undefined && countShared > 0" class="item-count">{{ countShared }}</span>
        </div>
        <template v-if="organizationsWithCount.length">
          <div class="sidebar-section">
            <span class="section-title">{{ $t('listSpaceSidebar.spaces') }}</span>
          </div>
          <div
            v-for="org in organizationsWithCount"
            :key="org.id"
            class="sidebar-item org-item"
            :class="{ active: selected === org.id }"
            @click="select(org.id)"
          >
            <div class="item-left">
              <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" class="item-avatar" />
              <span class="item-label" :title="org.name">{{ org.name }}</span>
            </div>
            <span v-if="getOrgCount(org.id) !== undefined" class="item-count">{{ getOrgCount(org.id) }}</span>
          </div>
        </template>
      </template>

      <template v-else>
        <div
          class="sidebar-item"
          :class="{ active: selected === 'created' }"
          @click="select('created')"
        >
          <div class="item-left">
            <t-icon name="usergroup-add" class="item-icon" />
            <span class="item-label">{{ $t('organization.createdByMe') }}</span>
          </div>
          <span v-if="countCreated !== undefined" class="item-count">{{ countCreated }}</span>
        </div>
        <div
          class="sidebar-item"
          :class="{ active: selected === 'joined' }"
          @click="select('joined')"
        >
          <div class="item-left">
            <t-icon name="usergroup" class="item-icon" />
            <span class="item-label">{{ $t('organization.joinedByMe') }}</span>
          </div>
          <span v-if="countJoined !== undefined" class="item-count">{{ countJoined }}</span>
        </div>
      </template>
    </nav>

    <!-- Drag handle on the right edge -->
    <div
      class="resize-handle"
      @mousedown.prevent="onDragStart"
    >
      <div class="resize-handle-line" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { Icon as TIcon } from 'tdesign-vue-next'
import SpaceAvatar from './SpaceAvatar.vue'
import { useOrganizationStore } from '@/stores/organization'

const COLLAPSED_WIDTH = 56
const EXPANDED_WIDTH = 208
const SNAP_THRESHOLD = 120

const props = withDefaults(
  defineProps<{
    mode?: 'resource' | 'organization'
    modelValue: string
    collapsedKey?: string
    countAll?: number
    countMine?: number
    countShared?: number
    countByOrg?: Record<string, number>
    countCreated?: number
    countJoined?: number
    hideAll?: boolean
    hideShared?: boolean
  }>(),
  { mode: 'resource', collapsedKey: 'sidebar-collapsed-list', countAll: undefined, countMine: undefined, countShared: undefined, countByOrg: () => ({}), countCreated: undefined, countJoined: undefined, hideAll: false, hideShared: false }
)

const storageKey = props.collapsedKey + '-expanded'
const sidebarRef = ref<HTMLElement | null>(null)
const isExpanded = ref(localStorage.getItem(storageKey) === 'true')
const isDragging = ref(false)
const dragWidth = ref(isExpanded.value ? EXPANDED_WIDTH : COLLAPSED_WIDTH)

let startX = 0
let startWidth = 0

function onDragStart(e: MouseEvent) {
  isDragging.value = true
  startX = e.clientX
  startWidth = isExpanded.value ? EXPANDED_WIDTH : COLLAPSED_WIDTH
  dragWidth.value = startWidth
  document.addEventListener('mousemove', onDragMove)
  document.addEventListener('mouseup', onDragEnd)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function onDragMove(e: MouseEvent) {
  const delta = e.clientX - startX
  const newWidth = Math.max(COLLAPSED_WIDTH, Math.min(EXPANDED_WIDTH + 20, startWidth + delta))
  dragWidth.value = newWidth
}

function onDragEnd() {
  document.removeEventListener('mousemove', onDragMove)
  document.removeEventListener('mouseup', onDragEnd)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''

  const shouldExpand = dragWidth.value >= SNAP_THRESHOLD
  isExpanded.value = shouldExpand
  localStorage.setItem(storageKey, String(shouldExpand))
  isDragging.value = false
  dragWidth.value = shouldExpand ? EXPANDED_WIDTH : COLLAPSED_WIDTH
}

function tooltipText(name: string, count?: number): string {
  return count !== undefined ? `${name} (${count})` : name
}

function truncateLabel(text: string, max = 4): string {
  return text.length > max ? text.slice(0, max) : text
}

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const orgStore = useOrganizationStore()
const selected = computed({
  get: () => props.modelValue,
  set: (v: string) => emit('update:modelValue', v)
})

const organizations = computed(() => orgStore.organizations || [])

const organizationsWithCount = computed(() => {
  if (props.mode !== 'resource') return organizations.value
  return organizations.value.filter((org) => (props.countByOrg?.[org.id] ?? 0) > 0)
})

function select(value: string) {
  selected.value = value
}

function getOrgCount(orgId: string): number | undefined {
  const n = props.countByOrg?.[orgId]
  return n === undefined ? undefined : n
}

onMounted(() => {
  orgStore.fetchOrganizations()
})

onBeforeUnmount(() => {
  document.removeEventListener('mousemove', onDragMove)
  document.removeEventListener('mouseup', onDragEnd)
})
</script>

<style scoped lang="less">
.list-space-sidebar {
  width: 56px;
  flex-shrink: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 0;
  z-index: 10;
  transition: width 0.25s cubic-bezier(0.4, 0, 0.2, 1);

  &.expanded {
    width: 208px;
    margin-right: 0;
  }

  &.dragging {
    transition: none;
  }
}

/* ========== Drag handle ========== */
.resize-handle {
  position: absolute;
  top: 0;
  right: -4px;
  bottom: 0;
  width: 8px;
  cursor: col-resize;
  z-index: 12;
  display: flex;
  align-items: center;
  justify-content: center;

  &:hover .resize-handle-line,
  .dragging & .resize-handle-line {
    opacity: 1;
    background: #07c05f;
  }
}

.resize-handle-line {
  width: 2px;
  height: 32px;
  border-radius: 1px;
  background: #c5cad1;
  opacity: 0;
  transition: opacity 0.2s ease, background 0.2s ease;
}

/* ========== Icon strip (collapsed) ========== */
.icon-strip {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  width: 56px;
  padding: 16px 0 8px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.icon-item-labeled {
  width: 46px;
  padding: 6px 0 3px;
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  cursor: pointer;
  color: #6b7482;
  transition: all 0.15s ease;
  flex-shrink: 0;

  &:hover {
    background: #f5f7fa;
    color: #1d2129;
  }

  &.active {
    background: #eef9f2;
    color: #07c05f;

    &:hover {
      background: #e4f5ea;
    }

    .icon-label {
      color: #07c05f;
      font-weight: 520;
    }
  }

  :deep(.space-avatar) {
    width: 20px;
    height: 20px;
    font-size: 10px;
  }
}

.icon-label {
  font-size: 10px;
  line-height: 1.2;
  color: #86909c;
  max-width: 44px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: center;
  transition: color 0.15s ease;
}

.icon-strip-divider {
  width: 24px;
  height: 1px;
  background: #e7ebf0;
  margin: 4px 0;
  flex-shrink: 0;
}

/* ========== Expanded panel ========== */
.expanded-panel {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 16px 10px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;
  border-right: 1px solid #eef1f5;

  &::-webkit-scrollbar {
    display: none;
  }
}

/* ========== Nav items inside expanded panel ========== */
.sidebar-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 7px;
  color: #3f4652;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: "PingFang SC", -apple-system, BlinkMacSystemFont, sans-serif;
  font-size: 14px;
  -webkit-font-smoothing: antialiased;

  .item-left {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    flex: 1;
  }

  .item-icon {
    flex-shrink: 0;
    color: #5c6470;
    font-size: 14px;
    transition: color 0.15s ease;
  }

  .item-avatar {
    flex-shrink: 0;
  }

  .item-label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
    font-weight: 430;
    line-height: 1.4;
    letter-spacing: 0.01em;
  }

  .item-count {
    font-size: 12px;
    color: #6b7482;
    font-weight: 500;
    padding: 2px 7px;
    border-radius: 8px;
    background: #f4f6f8;
    margin-left: 6px;
    flex-shrink: 0;
    transition: all 0.15s ease;
  }

  &:hover {
    background: #f5f7fa;
    color: #1d2129;

    .item-icon {
      color: #1d2129;
    }

    .item-count {
      background: #edf1f5;
      color: #1d2129;
    }
  }

  &.active {
    background: #eef9f2;
    color: #07c05f;

    .item-icon {
      color: #07c05f;
    }

    .item-label {
      font-weight: 500;
    }

    .item-count {
      background: #e4f5ea;
      color: #2f9d67;
      font-weight: 520;
    }

    &:hover {
      background: #e4f5ea;
    }
  }
}

.sidebar-section {
  padding: 10px 8px 3px;
  margin-top: 2px;
  border-top: 1px solid #eef1f5;

  .section-title {
    font-size: 12px;
    color: #86909c;
    font-weight: 600;
    line-height: 1.4;
  }
}
</style>
