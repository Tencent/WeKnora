<template>
  <aside class="list-space-sidebar">
    <div class="sidebar-header-row">
      <span class="sidebar-title">{{ $t('listSpaceSidebar.title') }}</span>
      <div v-if="$slots.actions" class="sidebar-actions">
        <slot name="actions" />
      </div>
    </div>
    <nav class="sidebar-nav">
      <!-- 全部 -->
      <div
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
      <!-- 资源列表模式：我的 + 空间列表 -->
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
        <template v-if="organizations.length">
          <div class="sidebar-section">
            <span class="section-title">{{ $t('listSpaceSidebar.spaces') }}</span>
          </div>
          <div
            v-for="org in organizations"
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
    /** resource = 知识库/智能体（全部+我的+空间列表）；organization = 共享空间（全部+我创建的+我加入的） */
    mode?: 'resource' | 'organization'
    modelValue: string
    /** 全部数量（可选） */
    countAll?: number
    /** 我的数量（resource 模式） */
    countMine?: number
    /** 各空间下的数量（resource 模式），key 为 organization_id */
    countByOrg?: Record<string, number>
    /** 我创建的数量（organization 模式） */
    countCreated?: number
    /** 我加入的数量（organization 模式） */
    countJoined?: number
  }>(),
  { mode: 'resource', countAll: undefined, countMine: undefined, countByOrg: () => ({}), countCreated: undefined, countJoined: undefined }
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
// 作为整体内容区左侧一栏，与主内容同属一块区域
.list-space-sidebar {
  width: 176px;
  flex-shrink: 0;
  background: #fafbfc;
  border-right: 1px solid #e7ebf0;
  padding: 12px;
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
  margin-bottom: 8px;
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
  :deep(.t-button .t-icon + .t-button__text:not(:empty)) {
    margin-left: 0;
  }
  :deep(.sidebar-org-icon) {
    width: 16px;
    height: 16px;
    filter: brightness(0) saturate(100%) invert(55%);
  }
  :deep(.t-button:hover .sidebar-org-icon) {
    filter: brightness(0) saturate(100%) invert(40%);
  }
}

.sidebar-nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
}

.sidebar-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 8px;
  border-radius: 6px;
  color: #4e5969;
  cursor: pointer;
  transition: all 0.2s ease;
  font-size: 12px;

  .item-left {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    flex: 1;
  }

  .item-icon {
    flex-shrink: 0;
    color: #86909c;
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
    font-weight: 400;
  }

  .item-count {
    font-size: 12px;
    color: #86909c;
    font-weight: 500;
    padding: 2px 5px;
    border-radius: 8px;
    background: #f7f9fc;
    margin-left: 6px;
    flex-shrink: 0;
    transition: all 0.2s ease;
  }

  &:hover {
    background: #f7f9fc;
    color: #1d2129;

    .item-icon {
      color: #4e5969;
    }

    .item-count {
      background: #e5e9f2;
      color: #4e5969;
    }
  }

  &.active {
    background: #e6f7ec;
    color: #00a870;
    font-weight: 500;

    .item-icon {
      color: #00a870;
    }

    .item-count {
      background: #b8f0d3;
      color: #00a870;
      font-weight: 600;
    }

    &:hover {
      background: #d4f4e3;
    }
  }
}

.sidebar-section {
  padding: 8px 8px 2px;
  margin-top: 2px;
  border-top: 1px solid #e7ebf0;

  .section-title {
    font-size: 12px;
    color: #86909c;
    font-weight: 600;
    line-height: 1.4;
  }
}
</style>
