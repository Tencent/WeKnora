<template>
  <aside class="list-space-sidebar" :class="{ collapsed }">
    <!-- 折叠/展开按钮：浮在右侧边缘垂直居中 -->
    <div class="sidebar-toggle" @click="toggleCollapse">
      <t-icon :name="collapsed ? 'chevron-right' : 'chevron-left'" size="14px" />
    </div>

    <!-- ========== 折叠态：44px 图标工具条 ========== -->
    <template v-if="collapsed">
      <div class="icon-strip">
        <!-- 组织模式：全部 -->
        <t-tooltip v-if="mode !== 'resource'" :content="tooltipText($t('listSpaceSidebar.all'), countAll)" placement="right">
          <div class="icon-item" :class="{ active: selected === 'all' }" @click="select('all')">
            <t-icon name="layers" size="16px" />
          </div>
        </t-tooltip>

        <!-- 资源模式：我的 + 共享给我 + 空间 -->
        <template v-if="mode === 'resource'">
          <t-tooltip :content="tooltipText($t('listSpaceSidebar.mine'), countMine)" placement="right">
            <div class="icon-item" :class="{ active: selected === 'mine' }" @click="select('mine')">
              <t-icon name="user" size="16px" />
            </div>
          </t-tooltip>
          <t-tooltip v-if="countShared !== undefined && countShared > 0" :content="tooltipText($t('listSpaceSidebar.sharedToMe'), countShared)" placement="right">
            <div class="icon-item" :class="{ active: selected === 'shared' }" @click="select('shared')">
              <t-icon name="share" size="16px" />
            </div>
          </t-tooltip>
          <template v-if="organizationsWithCount.length">
            <div class="icon-strip-divider" />
            <t-tooltip v-for="org in organizationsWithCount" :key="org.id" :content="tooltipText(org.name, getOrgCount(org.id))" placement="right">
              <div class="icon-item" :class="{ active: selected === org.id }" @click="select(org.id)">
                <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
              </div>
            </t-tooltip>
          </template>
        </template>

        <!-- 组织模式：我创建的 + 我加入的 -->
        <template v-else>
          <t-tooltip :content="tooltipText($t('organization.createdByMe'), countCreated)" placement="right">
            <div class="icon-item" :class="{ active: selected === 'created' }" @click="select('created')">
              <t-icon name="usergroup-add" size="16px" />
            </div>
          </t-tooltip>
          <t-tooltip :content="tooltipText($t('organization.joinedByMe'), countJoined)" placement="right">
            <div class="icon-item" :class="{ active: selected === 'joined' }" @click="select('joined')">
              <t-icon name="usergroup" size="16px" />
            </div>
          </t-tooltip>
        </template>
      </div>
    </template>

    <!-- ========== 展开态：200px 完整侧边栏 ========== -->
    <template v-else>
      <nav class="sidebar-nav">
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
    </template>
  </aside>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Icon as TIcon } from 'tdesign-vue-next'
import SpaceAvatar from './SpaceAvatar.vue'
import { useOrganizationStore } from '@/stores/organization'

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
  }>(),
  { mode: 'resource', collapsedKey: 'sidebar-collapsed-list', countAll: undefined, countMine: undefined, countShared: undefined, countByOrg: () => ({}), countCreated: undefined, countJoined: undefined }
)

const collapsed = ref(localStorage.getItem(props.collapsedKey) === 'true')

function toggleCollapse() {
  collapsed.value = !collapsed.value
  localStorage.setItem(props.collapsedKey, String(collapsed.value))
}

function tooltipText(name: string, count?: number): string {
  return count !== undefined ? `${name} (${count})` : name
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
</script>

<style scoped lang="less">
.list-space-sidebar {
  position: relative;
  width: 200px;
  flex-shrink: 0;
  background: #fff;
  padding: 24px 16px 16px;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: visible;
  transition: width 0.2s ease, padding 0.2s ease;

  &.collapsed {
    width: 44px;
    padding: 24px 0 8px;
    align-items: center;
  }
}

/* 浮动折叠/展开按钮 */
.sidebar-toggle {
  position: absolute;
  top: 50%;
  right: -10px;
  transform: translateY(-50%);
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #fff;
  border: 1px solid #e5e9f2;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  z-index: 2;
  color: #86909c;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
  transition: all 0.2s ease;
  opacity: 0;

  .list-space-sidebar:hover & {
    opacity: 1;
  }

  &:hover {
    background: #f2f4f7;
    color: #1d2129;
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.12);
  }
}

/* ========== 折叠态图标工具条 ========== */
.icon-strip {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  width: 100%;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.icon-item {
  width: 32px;
  height: 32px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: #5c6470;
  transition: all 0.15s ease;
  flex-shrink: 0;

  &:hover {
    background: #f2f4f7;
    color: #1d2129;
  }

  &.active {
    background: #e6f7ec;
    color: #07c05f;

    &:hover {
      background: #d4f4e3;
    }
  }

  :deep(.space-avatar) {
    width: 20px;
    height: 20px;
    font-size: 10px;
  }
}

.icon-strip-divider {
  width: 20px;
  height: 1px;
  background: #e7ebf0;
  margin: 4px 0;
  flex-shrink: 0;
}

/* ========== 展开态 ========== */

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
