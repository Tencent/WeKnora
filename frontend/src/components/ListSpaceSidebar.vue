<template>
  <aside class="list-space-sidebar">
    <div class="sidebar-header-row">
      <span class="sidebar-title">{{ $t('listSpaceSidebar.title') }}</span>
      <div v-if="$slots.actions" class="sidebar-actions">
        <slot name="actions" />
      </div>
    </div>
    <nav class="sidebar-nav">
      <!-- 全部：仅组织模式显示；知识库/智能体列表不展示「全部」 -->
      <div
        v-if="mode !== 'resource'"
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
      <!-- 资源列表模式：我的 + 共享给我 + 空间列表 -->
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
          v-if="countShared !== undefined && countShared > 0"
          class="sidebar-item"
          :class="{ active: selected === 'shared' }"
          @click="select('shared')"
        >
          <div class="item-left">
            <t-icon name="share" class="item-icon" />
            <span class="item-label">{{ $t('listSpaceSidebar.sharedToMe') }}</span>
          </div>
          <span class="item-count">{{ countShared }}</span>
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
      <!-- 共享空间列表模式：我创建的 + 我加入的 -->
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
  </aside>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Icon as TIcon } from 'tdesign-vue-next'
import SpaceAvatar from './SpaceAvatar.vue'
import { useOrganizationStore } from '@/stores/organization'

const props = withDefaults(
  defineProps<{
    /** resource = 知识库/智能体（全部+我的+共享给我+空间列表）；organization = 共享空间（全部+我创建的+我加入的） */
    mode?: 'resource' | 'organization'
    modelValue: string
    /** 全部数量（可选） */
    countAll?: number
    /** 我的数量（resource 模式） */
    countMine?: number
    /** 共享给我的数量（resource 模式） */
    countShared?: number
    /** 各空间下的数量（resource 模式），key 为 organization_id */
    countByOrg?: Record<string, number>
    /** 我创建的数量（organization 模式） */
    countCreated?: number
    /** 我加入的数量（organization 模式） */
    countJoined?: number
  }>(),
  { mode: 'resource', countAll: undefined, countMine: undefined, countShared: undefined, countByOrg: () => ({}), countCreated: undefined, countJoined: undefined }
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const orgStore = useOrganizationStore()
const selected = computed({
  get: () => props.modelValue,
  set: (v: string) => emit('update:modelValue', v)
})

const organizations = computed(() => orgStore.organizations || [])

/** 资源模式下只展示数量大于 0 的空间 */
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
</script>

<style scoped lang="less">
// 筛选区：白底卡片感（与右侧内容区风格对调），细分界保持统一
.list-space-sidebar {
  width: 200px;
  flex-shrink: 0;
  background: #fff;
  border-right: 1px solid #e7ebf0;
  box-shadow: 2px 0 8px rgba(0, 0, 0, 0.04);
  padding: 16px;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.sidebar-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
  flex-shrink: 0;
  min-height: 28px;

  .sidebar-title {
    font-size: 13px;
    font-weight: 600;
    line-height: 1.4;
    color: #1d2129;
  }
}

.sidebar-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;

  :deep(.t-button) {
    padding: 0;
    min-width: 24px;
    width: 24px;
    height: 24px;
    font-size: 12px;
    gap: 0;
    display: inline-flex !important;
    align-items: center !important;
    justify-content: center !important;
    background: #f2f3f5 !important;
    border: 1px solid #e5e9f2 !important;
    color: #4e5969;
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.2s, border-color 0.2s, color 0.2s;
    &:hover {
      background: #e5e9f2 !important;
      border-color: #c9cdd4 !important;
      color: #1d2129;
    }
  }
  :deep(.t-button .t-button__icon),
  :deep(.t-button .btn-icon-wrapper) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  :deep(.t-button .t-icon),
  :deep(.t-button .btn-icon-wrapper) {
    color: #07c05f;
  }
  :deep(.t-button:hover .t-icon),
  :deep(.t-button:hover .btn-icon-wrapper) {
    color: #07c05f;
  }
  :deep(.t-button .t-icon + .t-button__text:not(:empty)) {
    margin-left: 0;
  }
  :deep(.sidebar-org-icon) {
    width: 16px;
    height: 16px;
  }
}

.sidebar-nav {
  display: flex;
  flex-direction: column;
  gap: 5px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.sidebar-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 9px 12px;
  border-radius: 6px;
  color: #2d3139;
  cursor: pointer;
  transition: all 0.2s ease;
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
    transition: color 0.2s ease;
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
    font-family: "PingFang SC", -apple-system, BlinkMacSystemFont, sans-serif;
    font-size: 14px;
    font-weight: 450;
    line-height: 1.4;
    letter-spacing: 0.01em;
  }

  .item-count {
    font-size: 12px;
    color: #5c6470;
    font-weight: 500;
    padding: 3px 7px;
    border-radius: 8px;
    background: #eef0f3;
    margin-left: 6px;
    flex-shrink: 0;
    transition: all 0.2s ease;
  }

  &:hover {
    background: #f2f4f7;
    color: #1d2129;

    .item-icon {
      color: #1d2129;
    }

    .item-count {
      background: #e5e9f2;
      color: #1d2129;
    }
  }

  &.active {
    background: #e6f7ec;
    color: #07c05f;
    font-weight: 500;

    .item-icon {
      color: #07c05f;
    }

    .item-label {
      font-weight: 500;
    }

    .item-count {
      background: #b8f0d3;
      color: #07c05f;
      font-weight: 600;
    }

    &:hover {
      background: #d4f4e3;
    }
  }
}

.sidebar-section {
  padding: 10px 8px 2px;
  margin-top: 4px;
  border-top: 1px solid #e7ebf0;

  .section-title {
    font-size: 12px;
    color: #86909c;
    font-weight: 600;
    line-height: 1.4;
  }
}
</style>
