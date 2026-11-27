<template>
  <div class="form-item">
    <label class="form-label">{{ t('agentEditor.im.shareOriginals') }}</label>
    <t-switch v-model="enabled" :aria-label="t('agentEditor.im.shareOriginals')" :disabled="busy || !loaded" />
    <p class="form-desc">{{ t('agentEditor.im.shareOriginalsHint') }}</p>
    <t-select v-if="enabled" v-model="selected" :aria-label="t('agentEditor.im.fileKnowledgeBase')" :disabled="busy"
      filterable>
      <t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" />
    </t-select>
    <t-button v-if="loaded && (enabled ? selected !== saved || !saved : !!saved)" size="small"
      :loading="busy" :disabled="enabled && !selected" @click="save">{{ t('agentEditor.im.saveOriginalSharing') }}</t-button>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { getIMOriginalSharing, setIMOriginalSharing } from '@/api/agent'

const props = defineProps<{ channelId: string, knowledgeBases: { id: string, name: string }[] }>()
const { t } = useI18n()
const loaded = ref(false)
const busy = ref(false)
const saved = ref('')
const selected = ref('')
const enabled = ref(false)

onMounted(async () => {
  try {
    const res = await getIMOriginalSharing(props.channelId)
    saved.value = res.knowledge_base_id || ''
    selected.value = saved.value
    enabled.value = Boolean(saved.value)
    loaded.value = true
  } catch {
    MessagePlugin.error(t('common.operationFailed'))
  }
})

async function save() {
  busy.value = true
  const id = enabled.value ? selected.value : ''
  try {
    await setIMOriginalSharing(props.channelId, id)
    saved.value = id
    MessagePlugin.success(t('common.updateSuccess'))
  } catch {
    MessagePlugin.error(t('common.operationFailed'))
  } finally {
    busy.value = false
  }
}
</script>
