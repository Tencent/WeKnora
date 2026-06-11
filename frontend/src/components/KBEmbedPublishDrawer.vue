<template>
  <t-drawer
    :visible="visible"
    :header="$t('embedPublish.title')"
    size="680px"
    :footer="false"
    @close="$emit('update:visible', false)"
  >
    <div class="embed-publish">
      <p class="desc">{{ $t('embedPublish.description') }}</p>

      <t-button v-if="authStore.hasRole('admin')" theme="primary" @click="openCreate">
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
          {{ $t('embedPublish.agent') }}: {{ agentLabel(ch.agent_id) }} ·
          {{ $t('embedPublish.rateLimit') }} {{ ch.rate_limit_per_minute }}{{ $t('embedPublish.rateLimitUnit') }}
        </div>
        <div v-if="ch.allowed_origins?.length" class="meta">
          {{ $t('embedPublish.allowedOrigins') }}: {{ ch.allowed_origins.join(', ') }}
        </div>
        <div class="snippet-block">
          <t-tabs v-model="snippetTab[ch.id]" :default-value="'iframe'">
            <t-tab-panel value="iframe" :label="$t('embedPublish.tabIframe')">
              <label>{{ $t('embedPublish.embedCode') }}</label>
              <textarea readonly :value="iframeSnippet(ch)" rows="3" />
            </t-tab-panel>
            <t-tab-panel value="widget" :label="$t('embedPublish.tabWidget')">
              <label>{{ $t('embedPublish.widgetCode') }}</label>
              <textarea readonly :value="widgetSnippet(ch)" rows="4" />
            </t-tab-panel>
          </t-tabs>
          <div class="actions">
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
    </div>

    <t-dialog
      v-model:visible="showForm"
      :header="editingId ? $t('embedPublish.editTitle') : $t('embedPublish.createTitle')"
      :confirm-btn="$t('common.save')"
      :cancel-btn="$t('common.cancel')"
      @confirm="saveForm"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('embedPublish.name')">
          <t-input v-model="form.name" :placeholder="$t('embedPublish.namePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.agentLabel')">
          <t-select v-model="form.agent_id" :loading="agentsLoading">
            <t-option v-for="a in agents" :key="a.id" :value="a.id" :label="a.name" />
          </t-select>
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
          <t-input v-model="form.primary_color" placeholder="#0052d9" />
        </t-form-item>
        <t-form-item :label="$t('embedPublish.pageTitle')">
          <t-input v-model="form.page_title" :placeholder="$t('embedPublish.pageTitlePlaceholder')" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </t-drawer>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import { listAgents, BUILTIN_QUICK_ANSWER_ID, type CustomAgent } from '@/api/agent'
import {
  listEmbedChannels,
  createEmbedChannel,
  updateEmbedChannel,
  deleteEmbedChannel,
  rotateEmbedToken,
  buildEmbedSnippet,
  buildWidgetSnippet,
  type EmbedChannel,
} from '@/api/embed'

const props = defineProps<{ visible: boolean; kbId: string }>()
defineEmits<{ (e: 'update:visible', value: boolean): void }>()

const { t } = useI18n()
const authStore = useAuthStore()
const loading = ref(false)
const agentsLoading = ref(false)
const channels = ref<EmbedChannel[]>([])
const agents = ref<CustomAgent[]>([])
const tokenByChannel = ref<Record<string, string>>({})
const snippetTab = reactive<Record<string, string>>({})
const showForm = ref(false)
const editingId = ref('')
const originsText = ref('')

const defaultForm = () => ({
  name: '',
  agent_id: BUILTIN_QUICK_ANSWER_ID,
  welcome_message: '',
  rate_limit_per_minute: 30,
  primary_color: '#0052d9',
  page_title: '',
})
const form = ref(defaultForm())

const loadAgents = async () => {
  agentsLoading.value = true
  try {
    const res = await listAgents()
    agents.value = res?.data || []
  } finally {
    agentsLoading.value = false
  }
}

const load = async () => {
  if (!props.kbId) return
  loading.value = true
  try {
    const res = await listEmbedChannels(props.kbId)
    channels.value = res?.data || []
    for (const ch of channels.value) {
      if (!snippetTab[ch.id]) snippetTab[ch.id] = 'iframe'
    }
  } finally {
    loading.value = false
  }
}

watch(() => [props.visible, props.kbId], () => {
  if (props.visible) {
    load()
    if (agents.value.length === 0) loadAgents()
  }
}, { immediate: true })

const agentLabel = (id: string) => agents.value.find((a) => a.id === id)?.name || id

const tokenFor = (ch: EmbedChannel) => tokenByChannel.value[ch.id] || ch.publish_token

const iframeSnippet = (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) return `<!-- ${t('embedPublish.tokenHint')} -->`
  return buildEmbedSnippet(ch.id, token)
}

const widgetSnippet = (ch: EmbedChannel) => {
  const token = tokenFor(ch)
  if (!token) return `<!-- ${t('embedPublish.tokenHint')} -->`
  return buildWidgetSnippet(ch.id, token, {
    primaryColor: ch.primary_color,
    title: ch.page_title || ch.name,
  })
}

const activeSnippet = (ch: EmbedChannel) =>
  snippetTab[ch.id] === 'widget' ? widgetSnippet(ch) : iframeSnippet(ch)

const openCreate = () => {
  editingId.value = ''
  form.value = defaultForm()
  originsText.value = ''
  showForm.value = true
}

const openEdit = (ch: EmbedChannel) => {
  editingId.value = ch.id
  form.value = {
    name: ch.name,
    agent_id: ch.agent_id || BUILTIN_QUICK_ANSWER_ID,
    welcome_message: ch.welcome_message,
    rate_limit_per_minute: ch.rate_limit_per_minute || 30,
    primary_color: ch.primary_color || '#0052d9',
    page_title: ch.page_title || '',
  }
  originsText.value = (ch.allowed_origins || []).join('\n')
  showForm.value = true
}

const parseOrigins = () => originsText.value.split('\n').map((s) => s.trim()).filter(Boolean)

const saveForm = async () => {
  const payload = {
    name: form.value.name,
    agent_id: form.value.agent_id,
    welcome_message: form.value.welcome_message,
    allowed_origins: parseOrigins(),
    rate_limit_per_minute: form.value.rate_limit_per_minute,
    primary_color: form.value.primary_color,
    page_title: form.value.page_title,
    enabled: true,
  }
  if (editingId.value) {
    await updateEmbedChannel(editingId.value, payload)
    MessagePlugin.success(t('embedPublish.updated'))
  } else {
    const res = await createEmbedChannel(props.kbId, payload)
    if (res?.data?.publish_token) {
      tokenByChannel.value[res.data.id] = res.data.publish_token
    }
    MessagePlugin.success(t('embedPublish.created'))
  }
  showForm.value = false
  await load()
}

const copySnippet = async (ch: EmbedChannel) => {
  await navigator.clipboard.writeText(activeSnippet(ch))
  MessagePlugin.success(t('embedPublish.copied'))
}

const rotate = async (id: string) => {
  const res = await rotateEmbedToken(id)
  if (res?.data?.publish_token) tokenByChannel.value[id] = res.data.publish_token
  await load()
  MessagePlugin.success(t('embedPublish.tokenRotated'))
}

const remove = async (id: string) => {
  await deleteEmbedChannel(id)
  await load()
  MessagePlugin.success(t('embedPublish.deleted'))
}

const toggleEnabled = async (ch: EmbedChannel, enabled: boolean) => {
  await updateEmbedChannel(ch.id, {
    name: ch.name,
    agent_id: ch.agent_id,
    welcome_message: ch.welcome_message,
    allowed_origins: ch.allowed_origins,
    rate_limit_per_minute: ch.rate_limit_per_minute,
    primary_color: ch.primary_color,
    page_title: ch.page_title,
    enabled,
  })
  await load()
}
</script>

<style scoped>
.embed-publish { display: flex; flex-direction: column; gap: 16px; }
.desc { color: var(--td-text-color-secondary); font-size: 14px; margin: 0; }
.empty { color: var(--td-text-color-placeholder); padding: 24px 0; }
.channel-card { border: 1px solid var(--td-component-border); border-radius: 8px; padding: 12px; }
.channel-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.head-actions { display: flex; align-items: center; gap: 4px; }
.meta { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 4px; }
.snippet-block textarea { width: 100%; font-family: monospace; font-size: 12px; margin-top: 8px; }
.actions { display: flex; gap: 8px; margin-top: 8px; flex-wrap: wrap; }
</style>
