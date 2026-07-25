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
      <t-popup
        v-model:visible="menuOpen"
        :overlay-class-name="menuOverlayClass"
        trigger="click"
        destroy-on-close
        placement="bottom-right"
        @visible-change="onMenuVisibleChange"
      >
        <button
          type="button"
          class="menu-more-wrap"
          aria-haspopup="menu"
          :aria-expanded="menuOpen"
          @click.stop
        >
          <t-icon name="ellipsis" class="menu-more" />
        </button>
        <template #content>
          <div class="session-action-menu" @click.stop>
            <template v-if="menuMode === 'menu'">
              <template v-for="(option, index) in menuOptions" :key="option.value">
                <div
                  v-if="shouldShowDividerBefore(option.value, index)"
                  class="session-action-menu__divider"
                />
                <button
                  type="button"
                  class="session-action-menu__item"
                  :class="{ 'is-danger': option.theme === 'error' }"
                  @click="handleMenuClick(option)"
                >
                  <component
                    :is="option.prefixIcon"
                    v-if="option.prefixIcon"
                    class="session-action-menu__icon"
                  />
                  <span>{{ option.content }}</span>
                </button>
              </template>
            </template>

            <div v-else class="session-action-confirm">
              <div class="session-action-confirm__title">
                {{ menuMode === 'clear' ? t('chatHeader.clearConfirmTitle') : t('chatHeader.deleteConfirmTitle') }}
              </div>
              <div class="session-action-confirm__body">
                {{ menuMode === 'clear' ? t('chatHeader.clearConfirmBody') : t('chatHeader.deleteConfirmBody') }}
              </div>
              <div class="session-action-confirm__footer">
                <button type="button" class="session-action-confirm__btn" @click="backToMenu">
                  {{ t('common.cancel') }}
                </button>
                <button
                  type="button"
                  class="session-action-confirm__btn is-danger"
                  @click="confirmDangerAction"
                >
                  {{ menuMode === 'clear' ? t('common.clear') : t('common.delete') }}
                </button>
              </div>
            </div>
          </div>
        </template>
      </t-popup>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { normalizeSessionTitleDraft, SESSION_TITLE_MAX_LENGTH } from './sessionTitleEdit'

interface SessionMenuOption {
  content: string
  value: string
  theme?: 'default' | 'success' | 'warning' | 'error' | 'primary'
  prefixIcon?: any
}

type MenuMode = 'menu' | 'clear' | 'delete'

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

const { t } = useI18n()

const menuOpen = ref(false)
const menuMode = ref<MenuMode>('menu')
const titleEditing = ref(false)
const titleDraft = ref('')
const titleInputRef = ref<HTMLInputElement | null>(null)

const menuOverlayClass = computed(() => (
  menuMode.value === 'menu'
    ? 'session-action-menu-popup'
    : 'session-action-menu-popup is-confirm'
))

const onMenuVisibleChange = (visible: boolean): void => {
  if (!visible) menuMode.value = 'menu'
}

const backToMenu = (): void => {
  menuMode.value = 'menu'
}

const shouldShowDividerBefore = (value: string, index: number): boolean => {
  if (index === 0) return false
  return value === 'clearMessages' || value === 'delete'
}

const startTitleEdit = (): void => {
  menuOpen.value = false
  menuMode.value = 'menu'
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
  // Enter 会先触发 form submit，随后 input blur 再进一次；必须同步退出编辑态防重入。
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
  if (option.value === 'clearMessages') {
    menuMode.value = 'clear'
    return
  }
  if (option.value === 'delete') {
    menuMode.value = 'delete'
    return
  }
  menuOpen.value = false
  menuMode.value = 'menu'
  emit('menu-click', { value: option.value })
}

const confirmDangerAction = (): void => {
  const value = menuMode.value === 'clear' ? 'clearMessages' : 'delete'
  menuOpen.value = false
  menuMode.value = 'menu'
  emit('menu-click', { value })
}
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
  border-radius: 5px;
  color: inherit;
  background: transparent;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}
</style>

<style lang="less">
.session-action-menu-popup {
  z-index: 3000 !important;

  .t-popup__content {
    padding: 4px !important;
    margin-top: 2px !important;
    min-width: 160px !important;
    width: max-content !important;
    border-radius: 8px !important;
    background: var(--td-bg-color-container) !important;
    border: 0.5px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.03),
      0 2px 6px rgba(0, 0, 0, 0.08) !important;
    overflow: hidden;
  }

  &.is-confirm .t-popup__content {
    padding: 12px !important;
    width: 260px !important;
    min-width: 260px !important;
  }
}

.session-action-menu {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 152px;
}

.session-action-menu__item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 32px;
  padding: 0 12px;
  border: 0;
  border-radius: 5px;
  color: var(--td-text-color-primary);
  background: transparent;
  font-size: 14px;
  line-height: 20px;
  text-align: left;
  white-space: nowrap;
  box-sizing: border-box;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-danger {
    color: var(--td-error-color-6);

    .session-action-menu__icon {
      color: var(--td-error-color-6);
    }

    &:hover {
      background: var(--td-error-color-1);
    }
  }
}

.session-action-menu__icon {
  flex: 0 0 auto;
  display: inline-flex;
  color: var(--td-text-color-secondary);

  .t-icon {
    font-size: 16px;
  }
}

.session-action-menu__divider {
  height: 1px;
  margin: 2px 6px;
  background: var(--td-component-stroke);
}

.session-action-confirm {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 236px;
}

.session-action-confirm__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
}

.session-action-confirm__body {
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1.5;
  word-break: break-word;
}

.session-action-confirm__footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 2px;
}

.session-action-confirm__btn {
  min-width: 60px;
  height: 30px;
  padding: 0 12px;
  border: 0.5px solid var(--td-component-stroke);
  border-radius: 6px;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  font-size: 14px;
  line-height: 28px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease, border-color 0.15s ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
  }

  &.is-danger {
    border-color: transparent;
    color: #fff;
    background: var(--td-error-color-6);

    &:hover:not(:disabled) {
      background: var(--td-error-color-5);
    }
  }
}

:root[theme-mode='dark'] .session-action-menu-popup .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 6px rgba(0, 0, 0, 0.2) !important;
}
</style>
