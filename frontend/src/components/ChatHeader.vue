<template>
  <header class="chat-header" :class="{ 'is-editing': titleEditing, 'is-docked': hasReferencesPanel }">
    <form
      v-if="titleEditing"
      class="chat-header__edit"
      @submit.prevent="submitTitleEdit"
      @click.stop
    >
      <input
        ref="titleInputRef"
        v-model="titleDraft"
        class="chat-header__edit-input"
        :maxlength="SESSION_TITLE_MAX_LENGTH"
        :disabled="busyAction === 'rename'"
        :placeholder="t('chatHeader.renamePlaceholder')"
        @keydown.esc.prevent="cancelTitleEdit"
        @blur="submitTitleEdit"
      />
    </form>
    <h1
      v-else
      class="chat-header__title"
      :title="displayTitle"
      @dblclick="startTitleEdit"
    >
      <t-icon v-if="session?.is_pinned" name="pin" size="12px" class="chat-header__pin" />
      <span class="chat-header__title-text">{{ displayTitle }}</span>
    </h1>
    <t-popup
      v-if="!titleEditing"
      v-model:visible="menuVisible"
      :overlay-class-name="menuOverlayClass"
      trigger="click"
      destroy-on-close
      placement="bottom-left"
      :disabled="!session || Boolean(busyAction)"
      @visible-change="onMenuVisibleChange"
    >
      <button
        type="button"
        class="chat-header__menu-btn"
        :class="{ 'is-loading': Boolean(busyAction) }"
        :disabled="!session || Boolean(busyAction)"
        :aria-label="t('chatHeader.moreActions')"
        @click.stop
      >
        <t-icon v-if="busyAction" name="loading" size="14px" class="chat-header__menu-loading" />
        <t-icon v-else name="ellipsis" size="16px" />
      </button>
      <template #content>
        <div class="chat-header-menu" @click.stop>
          <template v-if="menuMode === 'menu'">
            <button type="button" class="chat-header-menu__item" @click="onMenuAction(session?.is_pinned ? 'unpin' : 'pin')">
              <t-icon class="chat-header-menu__icon" :name="session?.is_pinned ? 'pin-filled' : 'pin'" />
              <span>{{ session?.is_pinned ? t('menu.unpin') : t('menu.pin') }}</span>
            </button>
            <button type="button" class="chat-header-menu__item" @click="onMenuAction('rename')">
              <t-icon class="chat-header-menu__icon" name="edit-1" />
              <span>{{ t('menu.renameSession') }}</span>
            </button>
            <div class="chat-header-menu__divider" />
            <button type="button" class="chat-header-menu__item" @click="onMenuAction('copyId')">
              <t-icon class="chat-header-menu__icon" name="copy" />
              <span>{{ t('chatHeader.copySessionId') }}</span>
            </button>
            <button type="button" class="chat-header-menu__item" @click="onMenuAction('copyLink')">
              <t-icon class="chat-header-menu__icon" name="link" />
              <span>{{ t('chatHeader.copyLink') }}</span>
            </button>
            <button type="button" class="chat-header-menu__item" @click="onMenuAction('copyMarkdown')">
              <t-icon class="chat-header-menu__icon" name="file-copy" />
              <span>{{ t('chatHeader.copyMarkdown') }}</span>
            </button>
            <button type="button" class="chat-header-menu__item" @click="onMenuAction('openNewWindow')">
              <t-icon class="chat-header-menu__icon" name="browse" />
              <span>{{ t('chatHeader.openNewWindow') }}</span>
            </button>
            <div class="chat-header-menu__divider" />
            <button type="button" class="chat-header-menu__item" @click="enterConfirmMode('clear')">
              <t-icon class="chat-header-menu__icon" name="clear" />
              <span>{{ t('menu.clearMessages') }}</span>
            </button>
            <button type="button" class="chat-header-menu__item is-danger" @click="enterConfirmMode('delete')">
              <t-icon class="chat-header-menu__icon" name="delete" />
              <span>{{ t('chatHeader.deleteSession') }}</span>
            </button>
          </template>

          <div v-else class="chat-header-confirm">
            <div class="chat-header-confirm__title">
              {{ menuMode === 'clear' ? t('chatHeader.clearConfirmTitle') : t('chatHeader.deleteConfirmTitle') }}
            </div>
            <div class="chat-header-confirm__body">
              {{ menuMode === 'clear' ? t('chatHeader.clearConfirmBody') : t('chatHeader.deleteConfirmBody') }}
            </div>
            <div class="chat-header-confirm__footer">
              <button type="button" class="chat-header-confirm__btn" :disabled="Boolean(busyAction)" @click="backToMenu">
                {{ t('common.cancel') }}
              </button>
              <button
                type="button"
                class="chat-header-confirm__btn is-danger"
                :disabled="Boolean(busyAction)"
                @click="menuMode === 'clear' ? submitClearMessages() : submitDeleteSession()"
              >
                {{ menuMode === 'clear' ? t('common.clear') : t('common.delete') }}
              </button>
            </div>
          </div>
        </div>
      </template>
    </t-popup>
  </header>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { getMessageList } from '@/api/chat'
import {
  clearSession,
  removeSession,
  renameSession,
  setSessionPinned,
} from './sessionMutations'
import { normalizeSessionTitleDraft, SESSION_TITLE_MAX_LENGTH } from './sessionTitleEdit'
import { buildSessionMarkdown, collectAllSessionMessages } from '@/utils/sessionMarkdown'

interface ChatHeaderSession {
  id: string
  title?: string
  description?: string
  tenant_id?: number | string
  is_pinned?: boolean
}

type MenuMode = 'menu' | 'clear' | 'delete'

const props = defineProps<{
  session: ChatHeaderSession | null
  hasReferencesPanel?: boolean
}>()

const { t } = useI18n()
const busyAction = ref('')
const menuVisible = ref(false)
const menuMode = ref<MenuMode>('menu')
const titleEditing = ref(false)
const titleDraft = ref('')
const titleInputRef = ref<HTMLInputElement | null>(null)

const displayTitle = computed(() => props.session?.title?.trim() || t('menu.newSession'))
const menuOverlayClass = computed(() => (
  menuMode.value === 'menu' ? 'chat-header-menu-popup' : 'chat-header-menu-popup is-confirm'
))

function onMenuVisibleChange(visible: boolean): void {
  if (!visible) menuMode.value = 'menu'
}

function enterConfirmMode(mode: 'clear' | 'delete'): void {
  menuMode.value = mode
}

function backToMenu(): void {
  if (busyAction.value) return
  menuMode.value = 'menu'
}

function onMenuAction(value: string): void {
  if (value === 'rename') {
    menuVisible.value = false
    startTitleEdit()
    return
  }
  menuVisible.value = false
  handleMenuClick({ value })
}

async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // Some browsers expose Clipboard API outside a permitted context.
      // Fall through to the legacy copy path before reporting a failure.
    }
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  textarea.remove()
  if (!copied) throw new Error('clipboard unavailable')
}

function currentSessionLink(): string {
  const url = new URL(window.location.href)
  url.search = ''
  url.hash = ''
  return url.toString()
}

function startTitleEdit(): void {
  if (!props.session || busyAction.value) return
  menuVisible.value = false
  titleDraft.value = props.session.title || ''
  titleEditing.value = true
  nextTick(() => {
    titleInputRef.value?.focus()
    titleInputRef.value?.select()
  })
}

function cancelTitleEdit(): void {
  titleEditing.value = false
  titleDraft.value = ''
}

async function submitTitleEdit(): Promise<void> {
  // Enter 会先触发 form submit，随后 input blur 再进一次；必须同步退出编辑态防重入。
  if (!titleEditing.value || busyAction.value) return
  const session = props.session
  if (!session) {
    cancelTitleEdit()
    return
  }

  const title = normalizeSessionTitleDraft(titleDraft.value)
  const currentTitle = normalizeSessionTitleDraft(session.title || '')
  titleEditing.value = false
  titleDraft.value = ''
  if (!title || title === currentTitle) return

  busyAction.value = 'rename'
  try {
    await renameSession(session.id, title, session.description || '')
    MessagePlugin.success(t('menu.renameSessionSuccess'))
  } catch {
    MessagePlugin.error(t('menu.renameSessionFailed'))
  } finally {
    busyAction.value = ''
  }
}

async function togglePin(pinned: boolean): Promise<void> {
  const session = props.session
  if (!session || busyAction.value) return
  busyAction.value = 'pin'
  try {
    await setSessionPinned(session.id, pinned)
    MessagePlugin.success(t(pinned ? 'chatHeader.pinSuccess' : 'chatHeader.unpinSuccess'))
  } catch {
    MessagePlugin.error(t(pinned ? 'menu.pinFailed' : 'menu.unpinFailed'))
  } finally {
    busyAction.value = ''
  }
}

async function copySessionId(): Promise<void> {
  if (!props.session) return
  try {
    await copyText(props.session.id)
    MessagePlugin.success(t('chatHeader.sessionIdCopied'))
  } catch {
    MessagePlugin.error(t('chatHeader.copyFailed'))
  }
}

async function copyLink(): Promise<void> {
  try {
    await copyText(currentSessionLink())
    MessagePlugin.success(t('chatHeader.linkCopied'))
  } catch {
    MessagePlugin.error(t('chatHeader.copyFailed'))
  }
}

async function copyMarkdown(): Promise<void> {
  const session = props.session
  if (!session || busyAction.value) return
  busyAction.value = 'markdown'
  try {
    const messages = await collectAllSessionMessages(async (beforeTime, limit) => {
      const response: any = await getMessageList({
        session_id: session.id,
        created_at: beforeTime,
        limit,
      })
      if (!response?.success || !Array.isArray(response.data)) {
        throw new Error(response?.message || 'failed to load session messages')
      }
      return response.data
    })
    const markdown = buildSessionMarkdown({
      sessionId: session.id,
      title: session.title || t('menu.newSession'),
      messages,
      labels: {
        sessionId: t('chatHeader.markdown.sessionId'),
        exportedAt: t('chatHeader.markdown.exportedAt'),
        user: t('chatHeader.markdown.user'),
        assistant: t('chatHeader.markdown.assistant'),
        attachments: t('chatHeader.markdown.attachments'),
        references: t('chatHeader.markdown.references'),
      },
    })
    await copyText(markdown)
    MessagePlugin.success(t('chatHeader.markdownCopied'))
  } catch {
    MessagePlugin.error(t('chatHeader.markdownCopyFailed'))
  } finally {
    busyAction.value = ''
  }
}

async function submitClearMessages(): Promise<void> {
  const session = props.session
  if (!session || busyAction.value) return
  busyAction.value = 'clear'
  try {
    await clearSession(session.id)
    menuVisible.value = false
    menuMode.value = 'menu'
    MessagePlugin.success(t('menu.clearMessagesSuccess'))
  } catch {
    MessagePlugin.error(t('menu.clearMessagesFailed'))
  } finally {
    busyAction.value = ''
  }
}

async function submitDeleteSession(): Promise<void> {
  const session = props.session
  if (!session || busyAction.value) return
  busyAction.value = 'delete'
  try {
    await removeSession(session.id)
    menuVisible.value = false
    menuMode.value = 'menu'
    MessagePlugin.success(t('chatHeader.deleteSuccess'))
  } catch {
    MessagePlugin.error(t('chat.deleteSessionFailed'))
  } finally {
    busyAction.value = ''
  }
}

function handleMenuClick(data: { value: string }): void {
  switch (data.value) {
    case 'pin': void togglePin(true); break
    case 'unpin': void togglePin(false); break
    case 'copyId': void copySessionId(); break
    case 'copyLink': void copyLink(); break
    case 'copyMarkdown': void copyMarkdown(); break
    case 'openNewWindow': window.open(currentSessionLink(), '_blank', 'noopener,noreferrer'); break
  }
}
</script>

<style scoped lang="less">
.chat-header {
  position: absolute;
  top: 10px;
  left: 12px;
  z-index: 6;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  max-width: min(280px, calc(100% - 24px));
  min-width: 0;
  padding: 2px 2px 2px 8px;
  border-radius: 8px;
  box-sizing: border-box;
  background: color-mix(in srgb, var(--td-bg-color-container) 88%, transparent);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  pointer-events: auto;

  &.is-editing {
    max-width: min(360px, calc(100% - 24px));
    padding: 2px;
  }

  @media (min-width: 960px) {
    &.is-docked {
      position: relative;
      top: auto;
      left: auto;
      align-self: stretch;
      z-index: 5;
      flex-shrink: 0;
      width: 100%;
      max-width: none;
      margin: 0;
      padding: 10px 12px;
      border-radius: 0;
      border-bottom: 1px solid var(--td-component-stroke);
      background: var(--td-bg-color-container);
      backdrop-filter: none;
      -webkit-backdrop-filter: none;
      box-sizing: border-box;
      transition: border-color 0.3s cubic-bezier(0.22, 0.61, 0.36, 1);

      &.is-editing {
        max-width: none;
        padding: 8px 12px;
      }
    }
  }
}

.chat-header__edit {
  flex: 1 1 auto;
  min-width: 0;
  width: 240px;
  max-width: 100%;
}

.chat-header__edit-input {
  width: 100%;
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--td-brand-color);
  border-radius: 5px;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  font-size: 14px;
  line-height: 26px;
  outline: none;
  box-sizing: border-box;
  box-shadow: 0 0 0 2px var(--td-brand-color-light);

  &:disabled {
    opacity: 0.7;
  }
}

.chat-header__title {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  margin: 0;
  padding: 0;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
  cursor: default;
}

.chat-header__title-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-header__pin {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);
}

.chat-header__menu-btn {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: 5px;
  color: var(--td-text-color-placeholder);
  background: transparent;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover:not(:disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &:active:not(:disabled) {
    background: var(--td-bg-color-container-active);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }

  &.is-loading {
    cursor: wait;
  }
}

.chat-header__menu-loading {
  animation: chat-header-spin 0.8s linear infinite;
}

@keyframes chat-header-spin {
  from {
    transform: rotate(0deg);
  }

  to {
    transform: rotate(360deg);
  }
}
</style>

<style lang="less">
.chat-header-menu-popup {
  z-index: 99 !important;

  .t-popup__content {
    padding: 4px !important;
    margin-top: 2px !important;
    min-width: 168px !important;
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

.chat-header-menu {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 160px;
}

.chat-header-confirm {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 236px;
}

.chat-header-confirm__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
}

.chat-header-confirm__body {
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1.5;
  word-break: break-word;
}

.chat-header-confirm__footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 2px;
}

.chat-header-confirm__btn {
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

  &:disabled {
    cursor: not-allowed;
    opacity: 0.55;
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

.chat-header-menu__item {
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

    .chat-header-menu__icon {
      color: var(--td-error-color-6);
    }

    &:hover {
      background: var(--td-error-color-1);
    }
  }
}

.chat-header-menu__icon {
  flex: 0 0 auto;
  font-size: 16px;
  color: var(--td-text-color-secondary);
}

.chat-header-menu__divider {
  height: 1px;
  margin: 2px 6px;
  background: var(--td-component-stroke);
}

:root[theme-mode='dark'] .chat-header-menu-popup .t-popup__content {
  background: rgba(36, 36, 36, 0.92) !important;
  border-color: rgba(255, 255, 255, 0.08) !important;
  box-shadow:
    0 0 0 0.5px rgba(255, 255, 255, 0.05),
    0 2px 6px rgba(0, 0, 0, 0.2) !important;
}
</style>
