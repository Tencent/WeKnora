<template>
  <div class="embed-section">
    <div class="token-help">
      <p>{{ $t('embedPublish.publishTokenHelp') }}</p>
      <p>{{ $t('embedPublish.sessionTokenHelp') }}</p>
      <p class="token-help-warn">{{ $t('embedPublish.rotateTokenHelp') }}</p>
    </div>

    <div class="channels-header">
      <span class="channels-title">{{ $t('embedPublish.create') }}</span>
      <span class="channels-count">{{ channels.length }}</span>
    </div>

    <t-button
      v-if="authStore.hasRole('admin')"
      theme="default"
      variant="dashed"
      block
      class="add-btn"
      @click="openCreate"
    >
      <t-icon name="add" /> {{ $t('embedPublish.create') }}
    </t-button>

    <t-loading v-if="loading" size="small" />
    <div v-else-if="channels.length === 0" class="empty">{{ $t('embedPublish.empty') }}</div>

    <div v-for="ch in channels" :key="ch.id" class="channel-card">
      <div class="channel-head">
        <strong>{{ ch.name || $t('embedPublish.unnamed') }}</strong>
        <div class="head-actions">
          <t-button v-if="authStore.hasRole('admin')" size="small" variant="text" @click="openEdit(ch)">
            {{ $t('embedPublish.edit') }}
          </t-button>
          <t-switch
            v-if="authStore.hasRole('admin')"
            :value="ch.enabled"
            size="small"
            @change="(v: boolean) => toggleEnabled(ch, v)"
          />
        </div>
      </div>
      <div class="meta">
        {{ $t('embedPublish.rateLimit') }} {{ ch.rate_limit_per_minute }}{{ $t('embedPublish.rateLimitUnit') }}
      </div>
      <div v-if="ch.allowed_origins?.length" class="meta">
        {{ $t('embedPublish.allowedOrigins') }}: {{ ch.allowed_origins.join(', ') }}
      </div>
      <div v-if="tokenFor(ch)" class="token-row">
        <span class="token-label">{{ $t('embedPublish.publishToken') }}</span>
        <code class="token-value">{{ revealedTokens[ch.id] ? tokenFor(ch) : maskToken(tokenFor(ch)!) }}</code>
        <t-button size="small" variant="text" @click="toggleReveal(ch.id)">
          {{ revealedTokens[ch.id] ? $t('embedPublish.hideToken') : $t('embedPublish.revealToken') }}
        </t-button>
        <t-button size="small" variant="text" @click="copyToken(ch)">
          {{ $t('embedPublish.copyToken') }}
        </t-button>
      </div>
      <div class="snippet-block">
        <t-tabs v-model="snippetTab[ch.id]" :default-value="'iframe'">
          <t-tab-panel value="iframe" :label="$t('embedPublish.tabIframe')">
            <label>{{ $t('embedPublish.embedCode') }}</label>
            <textarea readonly :value="iframeSnippet(ch)" rows="3" />
          </t-tab-panel>
          <t-tab-panel value="widget" :label="$t('embedPublish.tabWidget')">
            <label>{{ $t('embedPublish.widgetCode') }}</label>
            <textarea readonly :value="widgetSnippet(ch)" rows="5" />
          </t-tab-panel>
        </t-tabs>
        <div class="actions">
          <t-button size="small" theme="primary" variant="outline" @click="openPreview(ch)">
            {{ $t('embedPublish.preview') }}
          </t-button>
          <t-button size="small" @click="copySnippet(ch)">{{ $t('embedPublish.copyCode') }}</t-button>
          <t-button v-if="authStore.hasRole('admin')" size="small" variant="outline" @click="rotate(ch.id)">
            {{ $t('embedPublish.rotateToken') }}
          </t-button>
          <t-button v-if="authStore.hasRole('admin')" size="small" theme="danger" variant="text" @click="remove(ch.id)">
            {{ $t('embedPublish.delete') }}
          </t-button>
        </div>
      </div>
    </div>

    <t-dialog
      v-model:visible="showForm"
      :header="editingId ? $t('embedPublish.editTitle') : $t('embedPublish.createTitle')"
      :confirm-btn="$t('common.save')"
      :cancel-btn="$t('common.cancel')"
      width="560px"
      @confirm="saveForm"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('embedPublish.name')">
          <t-input v-model="form.name" :placeholder="$t('embedPublish.namePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.welcomeMessage')">
          <t-textarea v-model="form.welcome_message" :placeholder="$t('embedPublish.welcomePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.originsLabel')">
          <t-textarea v-model="originsText" :placeholder="$t('embedPublish.originsPlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.rateLimitLabel')">
          <t-input-number v-model="form.rate_limit_per_minute" :min="1" :max="600" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.primaryColor')">
          <t-color-picker v-model="form.primary_color" format="HEX" :color-modes="['monochrome']" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.pageTitle')">
          <t-input v-model="form.page_title" :placeholder="$t('embedPublish.pageTitlePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.widgetPosition')">
          <t-select v-model="form.widget_position" :options="positionOptions" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.widgetPreview')">
          <div class="widget-preview" :class="`pos-${form.widget_position}`">
            <div class="preview-surface">
              <button
                type="button"
                class="preview-launcher"
                :style="{ background: form.primary_color || '#0052d9' }"
                aria-hidden="true"
              >
                💬
              </button>
            </div>
          </div>
        </t-form-item>
      </t-form>
    </t-dialog>

    <EmbedChannelPreview
      v-model:visible="previewVisible"
      :channel-id="previewChannel?.id || ''"
      :token="previewToken"
      :title="previewChannel?.name || $t('embedPublish.preview')"
      :primary-color="previewChannel?.primary_color"
      :position="previewPosition"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import EmbedChannelPreview from '@/components/EmbedChannelPreview.vue'
import {
  listEmbedChannels,
  createEmbedChannel,
  updateEmbedChannel,
  deleteEmbedChannel,
  rotateEmbedToken,
  buildEmbedSnippet,
  buildWidgetSnippet,
  type EmbedChannel,
  type WidgetPosition,
} from '@/api/embed'

const props = defineProps<{ agentId: string }>()

const { t } = useI18n()
const authStore = useAuthStore()
const loading = ref(false)
const channels = ref<EmbedChannel[]>([])
const tokenByChannel = ref<Record<string, string>>({})
const revealedTokens = reactive<Record<string, boolean>>({})
const previewVisible = ref(false)
const previewChannel = ref<EmbedChannel | null>(null)
const previewToken = ref('')
const snippetTab = reactive<Record<string, string>>({})
const showForm = ref(false)
const editingId = ref('')
const editingEnabled = ref(true)
const originsText = ref('')

const defaultForm = () => ({
  name: '',
  welcome_message: '',
  rate_limit_per_minute: 30,
  primary_color: '#0052d9',
  page_title: '',
  widget_position: 'bottom-right' as WidgetPosition,
})
const form = ref(defaultForm())

const positionOptions = computed(() => ([
  { label: t('embedPublish.positionBottomRight'), value: 'bottom-right' },
  { label: t('embedPublish.positionBottomLeft'), value: 'bottom-left' },
  { label: t('embedPublish.positionTopRight'), value: 'top-right' },
  { label: t('embedPublish.positionTopLeft'), value: 'top-left' },
]))

const load = async () => {
  if (!props.agentId) return
  loading.value = true
  try {
    const res = await listEmbedChannels(props.agentId)
    channels.value = res?.data || []
    for (const ch of channels.value) {
      if (!snippetTab[ch.id]) snippetTab[ch.id] = 'iframe'
    }
  } finally {
    loading.value = false
  }
}

watch(() => props.agentId, () => {
  if (props.agentId) load()
}, { immediate: true })

const tokenFor = (ch: EmbedChannel) => tokenByChannel.value[ch.id] || ch.publish_token

const maskToken = (token: string) => {
  if (token.length <= 8) return '••••••••'
  return `${token.slice(0, 4)}••••${token.slice(-4)}`
}

const toggleReveal = (channelId: string) => {
  revealedTokens[channelId] = !revealedTokens[channelId]
}

const copyToken = async (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) {
    MessagePlugin.warning(t('embedPublish.tokenHint'))
    return
  }
  await navigator.clipboard.writeText(token)
  MessagePlugin.success(t('embedPublish.tokenCopied'))
}

const iframeSnippet = (ch: EmbedChannel) => buildEmbedSnippet(ch.id)

const previewPosition = computed((): WidgetPosition =>
  (previewChannel.value?.widget_position as WidgetPosition) || 'bottom-right')

const widgetSnippet = (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) return `<!-- ${t('embedPublish.tokenHint')} -->`
  const position = (ch.widget_position as WidgetPosition) || 'bottom-right'
  return buildWidgetSnippet(ch.id, token, {
    primaryColor: ch.primary_color,
    title: ch.page_title || ch.name,
    position,
  })
}

const activeSnippet = (ch: EmbedChannel) =>
  snippetTab[ch.id] === 'widget' ? widgetSnippet(ch) : iframeSnippet(ch)

const openCreate = () => {
  editingId.value = ''
  editingEnabled.value = true
  form.value = defaultForm()
  originsText.value = ''
  showForm.value = true
}

const openEdit = (ch: EmbedChannel) => {
  editingId.value = ch.id
  editingEnabled.value = ch.enabled
  form.value = {
    name: ch.name,
    welcome_message: ch.welcome_message,
    rate_limit_per_minute: ch.rate_limit_per_minute || 30,
    primary_color: ch.primary_color || '#0052d9',
    page_title: ch.page_title || '',
    widget_position: (ch.widget_position as WidgetPosition) || 'bottom-right',
  }
  originsText.value = (ch.allowed_origins || []).join('\n')
  showForm.value = true
}

const parseOrigins = () => originsText.value.split('\n').map((s) => s.trim()).filter(Boolean)

const saveForm = async () => {
  const payload = {
    name: form.value.name,
    welcome_message: form.value.welcome_message,
    allowed_origins: parseOrigins(),
    rate_limit_per_minute: form.value.rate_limit_per_minute,
    primary_color: form.value.primary_color,
    page_title: form.value.page_title,
    widget_position: form.value.widget_position,
    enabled: editingId.value ? editingEnabled.value : true,
  }
  if (editingId.value) {
    await updateEmbedChannel(editingId.value, payload)
    MessagePlugin.success(t('embedPublish.updated'))
  } else {
    const res = await createEmbedChannel(props.agentId, payload)
    if (res?.data?.publish_token) {
      tokenByChannel.value[res.data.id] = res.data.publish_token
      MessagePlugin.success(t('embedPublish.createdDebugHint'))
    } else {
      MessagePlugin.success(t('embedPublish.created'))
    }
  }
  showForm.value = false
  await load()
}

const openPreview = (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) {
    MessagePlugin.warning(t('embedPublish.tokenRequiredForPreview'))
    return
  }
  previewChannel.value = ch
  previewToken.value = token
  previewVisible.value = true
}

const copySnippet = async (ch: EmbedChannel) => {
  await navigator.clipboard.writeText(activeSnippet(ch))
  MessagePlugin.success(t('embedPublish.copied'))
}

const rotate = (id: string) => {
  const dialog = DialogPlugin.confirm({
    header: t('embedPublish.rotateConfirmTitle'),
    body: t('embedPublish.rotateConfirmBody'),
    confirmBtn: t('embedPublish.rotateToken'),
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.hide()
      const res = await rotateEmbedToken(id)
      if (res?.data?.publish_token) tokenByChannel.value[id] = res.data.publish_token
      await load()
      MessagePlugin.success(t('embedPublish.tokenRotated'))
    },
  })
}

const remove = async (id: string) => {
  await deleteEmbedChannel(id)
  await load()
  MessagePlugin.success(t('embedPublish.deleted'))
}

const toggleEnabled = async (ch: EmbedChannel, enabled: boolean) => {
  await updateEmbedChannel(ch.id, {
    name: ch.name,
    welcome_message: ch.welcome_message,
    allowed_origins: ch.allowed_origins,
    rate_limit_per_minute: ch.rate_limit_per_minute,
    primary_color: ch.primary_color,
    page_title: ch.page_title,
    widget_position: ch.widget_position,
    enabled,
  })
  await load()
}
</script>

<style scoped>
.embed-section { display: flex; flex-direction: column; gap: 12px; }
.token-help {
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  padding: 10px 12px;
}
.token-help p { margin: 0 0 6px; }
.token-help p:last-child { margin-bottom: 0; }
.token-help-warn { color: var(--td-warning-color); }
.channels-header { display: flex; align-items: center; gap: 8px; }
.channels-title { font-weight: 600; font-size: 15px; }
.channels-count { font-size: 12px; color: var(--td-text-color-placeholder); background: var(--td-bg-color-secondarycontainer); padding: 2px 8px; border-radius: 10px; }
.empty { color: var(--td-text-color-placeholder); padding: 12px 0; font-size: 13px; }
.channel-card { border: 1px solid var(--td-component-border); border-radius: 8px; padding: 12px; margin-top: 8px; }
.channel-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.head-actions { display: flex; align-items: center; gap: 4px; }
.meta { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 4px; }
.token-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 12px;
  margin-bottom: 8px;
}
.token-label { color: var(--td-text-color-secondary); }
.token-value {
  font-family: monospace;
  background: var(--td-bg-color-secondarycontainer);
  padding: 2px 6px;
  border-radius: 4px;
}
.snippet-block textarea { width: 100%; font-family: monospace; font-size: 12px; margin-top: 8px; }
.actions { display: flex; gap: 8px; margin-top: 8px; flex-wrap: wrap; }
.add-btn { margin-top: 4px; }
.widget-preview {
  border: 1px dashed var(--td-component-border);
  border-radius: 8px;
  padding: 8px;
  background: var(--td-bg-color-secondarycontainer);
}
.preview-surface {
  position: relative;
  height: 120px;
  border-radius: 6px;
  background: #f5f7fa;
  overflow: hidden;
}
.preview-launcher {
  position: absolute;
  width: 40px;
  height: 40px;
  border: none;
  border-radius: 50%;
  color: #fff;
  font-size: 18px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.18);
  cursor: default;
}
.pos-bottom-right .preview-launcher { right: 12px; bottom: 12px; }
.pos-bottom-left .preview-launcher { left: 12px; bottom: 12px; }
.pos-top-right .preview-launcher { right: 12px; top: 12px; }
.pos-top-left .preview-launcher { left: 12px; top: 12px; }
</style>
