<template>
  <header class="chat-header">
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
      <span ref="titleTextRef" class="chat-header__title-text">{{ displayTitle }}</span>
    </h1>
    <router-link
      v-if="!titleEditing && automation"
      class="chat-header__automation"
      :to="`/platform/automations/${encodeURIComponent(automation.id)}`"
      :title="t('chatHeader.automationOpen', { name: automation.name })"
    >
      <t-icon name="time" size="12px" />
      <span>{{ automation.name }}</span>
    </router-link>
    <span
      v-else-if="!titleEditing && session?.kind === 'automation'"
      class="chat-header__automation"
    >
      <t-icon name="time" size="12px" />
      <span>{{ t('chatHeader.automationTask') }}</span>
    </span>
    <span
      v-if="!titleEditing && workspaceLabel"
      class="chat-header__workspace"
    >{{ workspaceLabel }}</span>
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
        <div class="card-menu" @click.stop>
          <template v-if="menuMode === 'menu'">
            <button type="button" class="card-menu-item" @click="onMenuAction('rename')">
              <t-icon class="icon" name="edit-1" />
              <span>{{ t('menu.renameSession') }}</span>
            </button>
            <button type="button" class="card-menu-item" @click="enterGrantsMode">
              <t-icon class="icon" name="lock-on" />
              <span>{{ t('chatHeader.grants.menu') }}</span>
            </button>
            <button type="button" class="card-menu-item" @click="onMenuAction('copyMarkdown')">
              <t-icon class="icon" name="file-copy" />
              <span>{{ t('chatHeader.copyMarkdown') }}</span>
            </button>
            <button type="button" class="card-menu-item danger" @click="enterConfirmMode('delete')">
              <t-icon class="icon" name="delete" />
              <span>{{ t('chatHeader.deleteSession') }}</span>
            </button>
          </template>

          <div v-else-if="menuMode === 'grants'" class="chat-header-confirm">
            <div class="chat-header-confirm__title">{{ t('chatHeader.grants.menu') }}</div>
            <div class="chat-header-confirm__body">{{ t('chatHeader.grants.description') }}</div>
            <t-loading v-if="grantsLoading" size="small" />
            <div v-else-if="!grants.length" class="chat-header-grants__empty">{{ t('chatHeader.grants.empty') }}</div>
            <ul v-else class="chat-header-grants">
              <li v-for="grant in grants" :key="grant.id" class="chat-header-grants__item">
                <span class="chat-header-grants__label" :title="grant.label">{{ grant.label }}</span>
                <button type="button" class="chat-header-confirm__btn" :disabled="revokingId === grant.id"
                  @click="revokeGrant(grant)">
                  {{ t('chatHeader.grants.revoke') }}
                </button>
              </li>
            </ul>
            <div class="chat-header-confirm__footer">
              <button type="button" class="chat-header-confirm__btn" @click="backToMenu">
                {{ t('common.back') }}
              </button>
            </div>
          </div>

          <div v-else class="chat-header-confirm">
            <div class="chat-header-confirm__title">
              {{ t('chatHeader.deleteConfirmTitle') }}
            </div>
            <div class="chat-header-confirm__body">
              {{ t('chatHeader.deleteConfirmBody') }}
            </div>
            <div class="chat-header-confirm__footer">
              <button type="button" class="chat-header-confirm__btn" :disabled="Boolean(busyAction)" @click="backToMenu">
                {{ t('common.cancel') }}
              </button>
              <button
                type="button"
                class="chat-header-confirm__btn is-danger"
                :disabled="Boolean(busyAction)"
                @click="submitDeleteSession()"
              >
                {{ t('common.delete') }}
              </button>
            </div>
          </div>
        </div>
      </template>
    </t-popup>
  </header>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { copyToClipboard } from '@/utils/clipboard'
import { getMessageList } from '@/api/chat'
import { getAutomationBySession } from '@/api/automation'
import { listAgentGrants, revokeAgentGrant, type AgentGrant } from '@/api/mcp-service'
import {
  removeSession,
  renameSession,
} from './sessionMutations'
import { normalizeSessionTitleDraft, SESSION_TITLE_MAX_LENGTH } from './sessionTitleEdit'
import { buildSessionMarkdown, collectAllSessionMessages } from '@/utils/sessionMarkdown'
import { useSessionTitleMotion } from '@/composables/useSessionTitleMotion'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { hostWorkspaceHeaderText, shouldRenderHostProjectSettings } from '@/utils/hostWorkspace'

interface ChatHeaderSession {
  id: string
  title?: string
  description?: string
  kind?: string
  tenant_id?: number | string
  is_pinned?: boolean
  host_workspace_dir?: string
}

type MenuMode = 'menu' | 'delete' | 'grants'

const props = defineProps<{
  session: ChatHeaderSession | null
}>()

const { t } = useI18n()
const deploymentCapabilities = useDeploymentCapabilitiesStore()
const busyAction = ref('')
const menuVisible = ref(false)
const menuMode = ref<MenuMode>('menu')
const titleEditing = ref(false)
const titleDraft = ref('')
const titleInputRef = ref<HTMLInputElement | null>(null)

const displayTitle = computed(() => props.session?.title?.trim() || t('menu.newSession'))
const automation = ref<{ id: string; name: string } | null>(null)

watch(() => [props.session?.id, props.session?.kind] as const, async ([id, kind]) => {
  automation.value = null
  if (!id || kind !== 'automation') return
  try {
    const res = await getAutomationBySession(id)
    if (props.session?.id === id && res.data?.id) {
      automation.value = { id: res.data.id, name: res.data.name }
    }
  } catch {
    // The badge stays, without a link, when the automation is gone.
  }
}, { immediate: true })
const titleTextRef = ref<HTMLElement | null>(null)
useSessionTitleMotion(titleTextRef, () => props.session?.id, () => displayTitle.value)
const workspaceLabel = computed(() => {
  if (!props.session) return ''
  if (!shouldRenderHostProjectSettings(deploymentCapabilities.isSupported('settings.sandbox.host'))) {
    return ''
  }
  return hostWorkspaceHeaderText(props.session.host_workspace_dir, t('chatHeader.temporaryWorkspace'))
})
const menuOverlayClass = computed(() => (
  menuMode.value === 'menu' ? 'card-more chat-header-menu-popup' : 'card-more chat-header-menu-popup is-confirm'
))

function onMenuVisibleChange(visible: boolean): void {
  if (!visible) menuMode.value = 'menu'
}

function enterConfirmMode(mode: 'delete'): void {
  menuMode.value = mode
}

const grants = ref<AgentGrant[]>([])
const grantsLoading = ref(false)
const revokingId = ref('')

async function enterGrantsMode(): Promise<void> {
  const session = props.session
  if (!session) return
  menuMode.value = 'grants'
  grants.value = []
  grantsLoading.value = true
  try {
    grants.value = await listAgentGrants(session.id)
  } catch {
    MessagePlugin.error(t('chatHeader.grants.loadFailed'))
  } finally {
    grantsLoading.value = false
  }
}

async function revokeGrant(grant: AgentGrant): Promise<void> {
  if (revokingId.value) return
  revokingId.value = grant.id
  try {
    await revokeAgentGrant(grant.id)
    grants.value = grants.value.filter((g) => g.id !== grant.id)
    MessagePlugin.success(t('chatHeader.grants.revoked'))
  } catch {
    MessagePlugin.error(t('chatHeader.grants.revokeFailed'))
  } finally {
    revokingId.value = ''
  }
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
  if (value === 'copyMarkdown') void copyMarkdown()
}

async function copyText(text: string): Promise<void> {
  const ok = await copyToClipboard(text)
  if (!ok) throw new Error('clipboard unavailable')
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

</script>

<style scoped lang="less">
.chat-header {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  max-width: min(520px, calc(100% - 36px));
  min-width: 0;
  box-sizing: border-box;
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
  font-size: var(--app-text-base);
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
  font-size: var(--app-text-base);
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

.chat-header__automation {
  display: inline-flex;
  flex: 0 1 auto;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 160px;
  margin-left: 6px;
  padding: 0 8px;
  height: 22px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 22px;
  text-decoration: none;
  white-space: nowrap;

  span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer-hover);
  }
}

.chat-header__workspace {
  flex: 0 1 auto;
  min-width: 0;
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 0 6px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  font-weight: 400;
  line-height: 20px;
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
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

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
  animation: wk-spin 0.8s linear infinite;
}

</style>

<style lang="less">
.card-more.chat-header-menu-popup.is-confirm .t-popup__content {
  padding: 12px !important;
  width: 260px !important;
  min-width: 260px !important;
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
  font-size: var(--app-text-base);
  font-weight: 600;
  line-height: 20px;
}

.chat-header-confirm__body {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
  line-height: 1.5;
  word-break: break-word;
}

.chat-header-grants {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 240px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}

.chat-header-grants__item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.chat-header-grants__label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-header-grants__empty {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
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
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  font-size: var(--app-text-base);
  line-height: 28px;
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;

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

</style>
