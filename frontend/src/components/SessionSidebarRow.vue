<template>
  <div
    :class="[
      'submenu_item',
      !batchMode && activePath === item.path ? 'submenu_item_active' : '',
      batchMode && selectedIds.includes(item.id) ? 'submenu_item_selected' : '',
      batchMode ? 'submenu_item_batch' : '',
    ]"
    @mouseenter="emit('hover-in')"
    @mouseleave="emit('hover-out')"
    @click="batchMode ? emit('toggle-select') : emit('navigate')"
  >
    <t-checkbox
      v-if="batchMode"
      class="batch-checkbox"
      :checked="selectedIds.includes(item.id)"
      @click.stop
      @change="emit('toggle-select')"
    />
    <form
      v-if="titleEditing"
      class="session-title-edit"
      @submit.prevent="submitTitleEdit"
      @click.stop
    >
      <input
        ref="titleInputRef"
        v-model="titleDraft"
        class="session-title-edit__input"
        :maxlength="SESSION_TITLE_MAX_LENGTH"
        @keydown.esc.prevent="cancelTitleEdit"
        @blur="submitTitleEdit"
      />
    </form>
    <span v-else class="submenu_title" :class="batchMode ? 'submenu_title--batch' : ''" :title="item.title">
      <t-icon v-if="item.is_pinned" name="pin" class="submenu_pin_icon" />
      <span class="submenu_title-text">{{ item.title }}</span>
    </span>
    <div v-if="!batchMode" class="session-row-menu-wrap" @click.stop>
      <button
        ref="triggerRef"
        type="button"
        class="menu-more-wrap"
        aria-haspopup="menu"
        :aria-expanded="menuOpen"
        @click="toggleMenu"
      >
        <t-icon name="ellipsis" class="menu-more" />
      </button>
      <Teleport to="body">
        <div
          v-if="menuOpen"
          class="session-row-menu"
          role="menu"
          :style="menuStyle"
          @click.stop
        >
          <button
            v-for="option in menuOptions"
            :key="option.value"
            type="button"
            class="session-row-menu__item"
            :class="{ 'session-row-menu__item--error': option.theme === 'error' }"
            role="menuitem"
            @click="handleMenuClick(option)"
          >
            <component
              :is="option.prefixIcon"
              v-if="option.prefixIcon"
              class="session-row-menu__icon"
            />
            <span class="session-row-menu__text">{{ option.content }}</span>
          </button>
        </div>
      </Teleport>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, nextTick, ref } from 'vue'
import { normalizeSessionTitleDraft, SESSION_TITLE_MAX_LENGTH } from './sessionTitleEdit'

interface SessionMenuOption {
  content: string
  value: string
  theme?: 'default' | 'success' | 'warning' | 'error' | 'primary'
  prefixIcon?: any
}

const props = defineProps<{
  item: { id: string; path: string; title: string; is_pinned?: boolean }
  batchMode: boolean
  activePath: string
  selectedIds: string[]
  menuOptions: SessionMenuOption[]
  /** 渠道文件夹下的会话（样式与聊天区会话共用文案列对齐） */
  nested?: boolean
}>()

const emit = defineEmits<{
  (e: 'navigate'): void
  (e: 'toggle-select'): void
  (e: 'menu-click', data: { value: string }): void
  (e: 'rename-submit', data: { title: string }): void
  (e: 'hover-in'): void
  (e: 'hover-out'): void
}>()

const MENU_WIDTH = 132
const MENU_GAP = 4
const VIEWPORT_MARGIN = 8

const menuOpen = ref(false)
const titleEditing = ref(false)
const titleDraft = ref('')
const triggerRef = ref<HTMLButtonElement | null>(null)
const titleInputRef = ref<HTMLInputElement | null>(null)
const menuStyle = ref<Record<string, string>>({})

const updateMenuPosition = (): void => {
  const trigger = triggerRef.value
  if (!trigger) return
  const rect = trigger.getBoundingClientRect()
  const left = Math.max(
    VIEWPORT_MARGIN,
    Math.min(rect.right - MENU_WIDTH, window.innerWidth - MENU_WIDTH - VIEWPORT_MARGIN),
  )
  menuStyle.value = {
    top: `${rect.bottom + MENU_GAP}px`,
    left: `${left}px`,
  }
}

const removeListeners = (): void => {
  document.removeEventListener('click', closeMenu)
  window.removeEventListener('resize', closeMenu)
  window.removeEventListener('scroll', closeMenu, true)
}

const closeMenu = (): void => {
  menuOpen.value = false
  removeListeners()
}

const toggleMenu = (): void => {
  if (menuOpen.value) {
    closeMenu()
    return
  }
  updateMenuPosition()
  menuOpen.value = true
  nextTick(() => {
    document.addEventListener('click', closeMenu)
    window.addEventListener('resize', closeMenu)
    // 捕获阶段监听任意滚动容器，滚动时关闭以避免菜单与触发点错位
    window.addEventListener('scroll', closeMenu, true)
  })
}

const startTitleEdit = (): void => {
  closeMenu()
  titleDraft.value = props.item.title || ''
  titleEditing.value = true
  nextTick(() => {
    titleInputRef.value?.focus()
    titleInputRef.value?.select()
  })
}

const cancelTitleEdit = (): void => {
  titleEditing.value = false
  titleDraft.value = ''
}

const submitTitleEdit = (): void => {
  if (!titleEditing.value) return
  const nextTitle = normalizeSessionTitleDraft(titleDraft.value)
  const currentTitle = normalizeSessionTitleDraft(props.item.title || '')
  titleEditing.value = false
  titleDraft.value = ''
  if (!nextTitle || nextTitle === currentTitle) return
  emit('rename-submit', { title: nextTitle })
}

const handleMenuClick = (option: SessionMenuOption): void => {
  if (option.value === 'rename') {
    startTitleEdit()
    return
  }
  closeMenu()
  emit('menu-click', { value: option.value })
}

onBeforeUnmount(() => {
  removeListeners()
})
</script>

<style scoped lang="less">
.submenu_item {
  position: relative;
}

.session-row-menu-wrap {
  position: relative;
  flex: 0 0 auto;
}

.session-title-edit {
  flex: 1 1 auto;
  min-width: 0;
}

.session-title-edit__input {
  width: 100%;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--td-brand-color);
  border-radius: 5px;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  font-size: 14px;
  line-height: 24px;
  outline: none;
  box-shadow: 0 0 0 2px var(--td-brand-color-light);
}

.menu-more-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  color: inherit;
  background: transparent;
  cursor: pointer;
}

.session-row-menu {
  position: fixed;
  z-index: 3000;
  min-width: 132px;
  padding: 4px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
}

.session-row-menu__item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 32px;
  padding: 0 10px;
  border: 0;
  border-radius: 4px;
  color: var(--td-text-color-primary);
  background: transparent;
  font-size: 14px;
  line-height: 20px;
  text-align: left;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.session-row-menu__item--error {
  color: var(--td-error-color);
}

.session-row-menu__icon {
  flex: 0 0 auto;
  display: inline-flex;
}

.session-row-menu__text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
