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
    <span class="submenu_title" :class="batchMode ? 'submenu_title--batch' : ''" :title="item.title">
      <t-icon v-if="item.is_pinned" name="pin" class="submenu_pin_icon" />
      <span class="submenu_title-text">{{ item.title }}</span>
    </span>
    <div v-if="!batchMode" class="session-row-menu-wrap" @click.stop>
      <button
        type="button"
        class="menu-more-wrap"
        aria-haspopup="menu"
        :aria-expanded="menuOpen"
        @click="toggleMenu"
      >
        <t-icon name="ellipsis" class="menu-more" />
      </button>
      <div v-if="menuOpen" class="session-row-menu" role="menu">
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
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

interface SessionMenuOption {
  content: string
  value: string
  theme?: 'default' | 'success' | 'warning' | 'error' | 'primary'
  prefixIcon?: any
}

defineProps<{
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
  (e: 'hover-in'): void
  (e: 'hover-out'): void
}>()

const menuOpen = ref(false)

const closeMenu = (): void => {
  menuOpen.value = false
  document.removeEventListener('click', closeMenu)
}

const toggleMenu = (): void => {
  menuOpen.value = !menuOpen.value
  if (menuOpen.value) {
    document.addEventListener('click', closeMenu)
  } else {
    document.removeEventListener('click', closeMenu)
  }
}

const handleMenuClick = (option: SessionMenuOption): void => {
  closeMenu()
  emit('menu-click', { value: option.value })
}

onBeforeUnmount(() => {
  document.removeEventListener('click', closeMenu)
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
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  z-index: 30;
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
