<template>
  <t-drawer
    :visible="visible"
    header="发布到网站"
    size="640px"
    :footer="false"
    @close="$emit('update:visible', false)"
  >
    <div class="embed-publish">
      <p class="desc">创建嵌入渠道后，可将知识库对话以 iframe 形式挂到第三方网站。</p>

      <t-button v-if="authStore.hasRole('admin')" theme="primary" @click="openCreate">
        <t-icon name="add" /> 新建嵌入渠道
      </t-button>

      <t-loading v-if="loading" size="small" />
      <div v-else-if="channels.length === 0" class="empty">暂无嵌入渠道</div>

      <div v-for="ch in channels" :key="ch.id" class="channel-card">
        <div class="channel-head">
          <strong>{{ ch.name || '未命名渠道' }}</strong>
          <t-switch v-if="authStore.hasRole('admin')" :value="ch.enabled" size="small" @change="(v: boolean) => toggleEnabled(ch, v)" />
        </div>
        <div class="meta">Agent: {{ ch.agent_id }} · 限流 {{ ch.rate_limit_per_minute }}/分钟</div>
        <div v-if="ch.allowed_origins?.length" class="meta">域名白名单: {{ ch.allowed_origins.join(', ') }}</div>
        <div class="snippet-block">
          <label>嵌入代码</label>
          <textarea readonly :value="snippetFor(ch)" rows="3" />
          <div class="actions">
            <t-button size="small" @click="copySnippet(ch)">复制代码</t-button>
            <t-button v-if="authStore.hasRole('admin')" size="small" variant="outline" @click="rotate(ch.id)">轮换 Token</t-button>
            <t-button v-if="authStore.hasRole('admin')" size="small" theme="danger" variant="text" @click="remove(ch.id)">删除</t-button>
          </div>
        </div>
      </div>
    </div>

    <t-dialog v-model:visible="showCreate" header="新建嵌入渠道" :confirm-btn="'创建'" @confirm="create">
      <t-form label-align="top">
        <t-form-item label="名称">
          <t-input v-model="form.name" placeholder="例如：官网客服" />
        </t-form-item>
        <t-form-item label="欢迎语">
          <t-textarea v-model="form.welcome_message" placeholder="你好，有什么可以帮您？" />
        </t-form-item>
        <t-form-item label="域名白名单（每行一个，留空表示不限制）">
          <t-textarea v-model="originsText" placeholder="https://shop.example.com&#10;*.example.com" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listEmbedChannels,
  createEmbedChannel,
  updateEmbedChannel,
  deleteEmbedChannel,
  rotateEmbedToken,
  buildEmbedSnippet,
  type EmbedChannel,
} from '@/api/embed'

const props = defineProps<{ visible: boolean; kbId: string }>()
defineEmits<{ (e: 'update:visible', value: boolean): void }>()

const authStore = useAuthStore()
const loading = ref(false)
const channels = ref<EmbedChannel[]>([])
const tokenByChannel = ref<Record<string, string>>({})
const showCreate = ref(false)
const form = ref({ name: '', welcome_message: '' })
const originsText = ref('')

const load = async () => {
  if (!props.kbId) return
  loading.value = true
  try {
    const res = await listEmbedChannels(props.kbId)
    channels.value = res?.data || []
  } finally {
    loading.value = false
  }
}

watch(() => [props.visible, props.kbId], () => {
  if (props.visible) load()
}, { immediate: true })

const openCreate = () => {
  form.value = { name: '', welcome_message: '' }
  originsText.value = ''
  showCreate.value = true
}

const parseOrigins = () =>
  originsText.value.split('\n').map(s => s.trim()).filter(Boolean)

const create = async () => {
  const res = await createEmbedChannel(props.kbId, {
    name: form.value.name,
    welcome_message: form.value.welcome_message,
    allowed_origins: parseOrigins(),
    enabled: true,
  })
  if (res?.data?.publish_token) {
    tokenByChannel.value[res.data.id] = res.data.publish_token
  }
  showCreate.value = false
  await load()
  MessagePlugin.success('嵌入渠道已创建')
}

const snippetFor = (ch: EmbedChannel) => {
  const token = tokenByChannel.value[ch.id] || ch.publish_token
  if (!token) {
    return '<!-- Token 仅在创建或轮换时显示，请点击「轮换 Token」获取嵌入代码 -->'
  }
  return buildEmbedSnippet(ch.id, token)
}

const copySnippet = async (ch: EmbedChannel) => {
  await navigator.clipboard.writeText(snippetFor(ch))
  MessagePlugin.success('已复制嵌入代码')
}

const rotate = async (id: string) => {
  const res = await rotateEmbedToken(id)
  if (res?.data?.publish_token) {
    tokenByChannel.value[id] = res.data.publish_token
  }
  await load()
  MessagePlugin.success('Token 已轮换')
}

const remove = async (id: string) => {
  await deleteEmbedChannel(id)
  await load()
  MessagePlugin.success('已删除')
}

const toggleEnabled = async (ch: EmbedChannel, enabled: boolean) => {
  await updateEmbedChannel(ch.id, { ...ch, enabled })
  await load()
}
</script>

<style scoped>
.embed-publish { display: flex; flex-direction: column; gap: 16px; }
.desc { color: var(--td-text-color-secondary); font-size: 14px; margin: 0; }
.empty { color: var(--td-text-color-placeholder); padding: 24px 0; }
.channel-card { border: 1px solid var(--td-component-border); border-radius: 8px; padding: 12px; }
.channel-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.meta { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 4px; }
.snippet-block textarea { width: 100%; font-family: monospace; font-size: 12px; margin-top: 4px; }
.actions { display: flex; gap: 8px; margin-top: 8px; }
</style>
