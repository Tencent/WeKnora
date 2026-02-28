<template>
  <div
    class="list-space-sidebar"
    :class="{ pinned }"
    @mouseenter="onMouseEnter"
    @mouseleave="onMouseLeave"
  >
    <!-- Icon strip: hidden when pinned (panel takes over) -->
    <div v-show="!pinned" class="icon-strip">
      <template v-if="mode !== 'resource'">
        <t-tooltip :content="tooltipText($t('listSpaceSidebar.all'), countAll)" placement="right" :show-arrow="false">
          <div class="icon-item" :class="{ active: selected === 'all' }" @click="select('all')">
            <t-icon name="layers" size="16px" />
          </div>
        </t-tooltip>
      </template>

      <template v-if="mode === 'resource'">
        <t-tooltip :content="tooltipText($t('listSpaceSidebar.mine'), countMine)" placement="right" :show-arrow="false">
          <div class="icon-item" :class="{ active: selected === 'mine' }" @click="select('mine')">
            <t-icon name="user" size="16px" />
          </div>
        </t-tooltip>
        <t-tooltip v-if="countShared !== undefined && countShared > 0" :content="tooltipText($t('listSpaceSidebar.sharedToMe'), countShared)" placement="right" :show-arrow="false">
          <div class="icon-item" :class="{ active: selected === 'shared' }" @click="select('shared')">
            <t-icon name="share" size="16px" />
          </div>
        </t-tooltip>
        <template v-if="organizationsWithCount.length">
          <div class="icon-strip-divider" />
          <t-tooltip v-for="org in organizationsWithCount" :key="org.id" :content="tooltipText(org.name, getOrgCount(org.id))" placement="right" :show-arrow="false">
            <div class="icon-item" :class="{ active: selected === org.id }" @click="select(org.id)">
              <SpaceAvatar :name="org.name" :avatar="org.avatar" size="small" />
            </div>
          </t-tooltip>
        </template>
      </template>

      <template v-else>
        <t-tooltip :content="tooltipText($t('organization.createdByMe'), countCreated)" placement="right" :show-arrow="false">
          <div class="icon-item" :class="{ active: selected === 'created' }" @click="select('created')">
            <t-icon name="usergroup-add" size="16px" />
          </div>
        </t-tooltip>
        <t-tooltip :content="tooltipText($t('organization.joinedByMe'), countJoined)" placement="right" :show-arrow="false">
          <div class="icon-item" :class="{ active: selected === 'joined' }" @click="select('joined')">
            <t-icon name="usergroup" size="16px" />
          </div>
        </t-tooltip>
      </template>
    </div>

    <!-- Expanded panel: floats over content on hover, or stays when pinned -->
    <Transition name="sb-expand">
      <aside v-if="expanded || pinned" class="expanded-panel" :class="{ pinned }">
        <div class="panel-pin" @click.stop="togglePin">
          <t-tooltip :content="pinned ? $t('listSpaceSidebar.unpin', 'Unpin') : $t('listSpaceSidebar.pin', 'Pin')" placement="right" :show-arrow="false">
            <t-icon :name="pinned ? 'pin-filled' : 'pin'" size="14px" />
          </t-tooltip>
        </div>
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
      </aside>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
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

const pinStorageKey = props.collapsedKey + '-pinned'
const pinned = ref(localStorage.getItem(pinStorageKey) === 'true')
const expanded = ref(pinned.value)
let enterTimer: ReturnType<typeof setTimeout> | null = null
let leaveTimer: ReturnType<typeof setTimeout> | null = null

function togglePin() {
  pinned.value = !pinned.value
  localStorage.setItem(pinStorageKey, String(pinned.value))
  if (pinned.value) {
    expanded.value = true
  }
}

function onMouseEnter() {
  if (pinned.value) return
  if (leaveTimer) { clearTimeout(leaveTimer); leaveTimer = null }
  enterTimer = setTimeout(() => { expanded.value = true }, 180)
}

function onMouseLeave() {
  if (pinned.value) return
  if (enterTimer) { clearTimeout(enterTimer); enterTimer = null }
  leaveTimer = setTimeout(() => { expanded.value = false }, 250)
}

onBeforeUnmount(() => {
  if (enterTimer) clearTimeout(enterTimer)
  if (leaveTimer) clearTimeout(leaveTimer)
})

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
  width: 44px;
  flex-shrink: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 0;
  z-index: 10;
  transition: width 0.25s cubic-bezier(0.4, 0, 0.2, 1);

  &.pinned {
    width: 208px;
    margin-right: 16px;
  }
}

/* ========== Icon strip (always visible, 44px) ========== */
.icon-strip {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  width: 44px;
  padding: 20px 0 8px;
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
  border-radius: 7px;
  display: flex;
  align-items: center;
  justify-content: center;
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
  }

  :deep(.space-avatar) {
    width: 20px;
    height: 20px;
    font-size: 10px;
  }
}

.icon-strip-divider {
  width: 18px;
  height: 1px;
  background: #e7ebf0;
  margin: 4px 0;
  flex-shrink: 0;
}

/* ========== Expanded floating panel ========== */
.expanded-panel {
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: 208px;
  background: #fff;
  box-shadow: 3px 0 18px rgba(0, 0, 0, 0.07), 1px 0 0 #e7ebf0;
  border-radius: 0 10px 10px 0;
  padding: 30px 10px 16px 10px;
  z-index: 11;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }

  &.pinned {
    position: relative;
    width: 100%;
    flex: 1;
    box-shadow: none;
    border-right: 1px solid #eef1f5;
    border-radius: 0;
  }
}

/* Pin button */
.panel-pin {
  position: absolute;
  top: 8px;
  right: 8px;
  width: 24px;
  height: 24px;
  border-radius: 5px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: #b0b7c0;
  transition: all 0.15s ease;
  z-index: 1;

  &:hover {
    background: #f5f7fa;
    color: #4e5662;
  }

  .pinned & {
    color: #07c05f;

    &:hover {
      background: #eef9f2;
      color: #059b4c;
    }
  }
}

/* ========== Transition ========== */
.sb-expand-enter-active {
  transition: opacity 0.2s ease, transform 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.sb-expand-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.sb-expand-enter-from {
  opacity: 0;
  transform: translateX(-8px);
}

.sb-expand-leave-to {
  opacity: 0;
  transform: translateX(-6px);
}

/* ========== Nav items inside expanded panel ========== */
.sidebar-nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

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
