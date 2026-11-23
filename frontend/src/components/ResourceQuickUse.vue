<template>
  <t-button class="resource-quick-use" theme="primary" variant="text" size="small"
    :loading="opening" :disabled="disabled || opening"
    :aria-label="t('common.quickUseResource', { name: resourceName })"
    @click.stop="startChat" @keydown.stop>
    <template #icon><t-icon name="chat" /></template>
    {{ t('common.quickUse') }}
  </t-button>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { createSessions } from '@/api/chat/index'
import { BUILTIN_QUICK_ANSWER_ID } from '@/api/agent'
import { useMenuStore } from '@/stores/menu'

const props = defineProps<{
  resourceName: string
  knowledgeBaseId?: string
  agentId?: string
  sourceTenantId?: string | number
  disabled?: boolean
}>()
const emit = defineEmits<{ started: [] }>()
const router = useRouter()
const menu = useMenuStore()
const { t } = useI18n()
const opening = ref(false)

async function startChat() {
  if (opening.value || props.disabled || (!props.knowledgeBaseId && !props.agentId)) return
  opening.value = true
  try {
    const result = await createSessions({})
    if (!result?.data?.id) throw new Error('Session creation returned no ID')
    const id = result.data.id
    const now = new Date().toISOString()
    menu.updataMenuChildren({
      id, path: `chat/${id}`, title: t('createChat.newSessionTitle'),
      isMore: false, isNoTitle: true, created_at: now, updated_at: now,
    })
    menu.changeIsFirstSession(false)
    const query: Record<string, string> = { agent_id: props.agentId || BUILTIN_QUICK_ANSWER_ID }
    if (props.knowledgeBaseId) query.knowledge_base_id = props.knowledgeBaseId
    if (props.agentId && props.sourceTenantId != null) query.source_tenant_id = String(props.sourceTenantId)
    await router.push({ path: `/platform/chat/${id}`, query })
    emit('started')
  } catch {
    MessagePlugin.error(t('createChat.messages.createError'))
  } finally {
    opening.value = false
  }
}
</script>

<style scoped>
.resource-quick-use {
  flex-shrink: 0;
  margin-left: auto;
}
</style>
