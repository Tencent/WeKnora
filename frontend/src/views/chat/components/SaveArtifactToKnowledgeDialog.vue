<template>
  <t-dialog
    :visible="true"
    :header="t('agent.addToKnowledgeBase')"
    :confirm-btn="{
      content: t('agent.artifactDrawer.saveToKnowledge'),
      loading: saving,
      disabled: loading || loadFailed || !selectedDestination,
    }"
    :cancel-btn="t('common.cancel')"
    @confirm="confirmSave"
    @close="close"
  >
    <p class="artifact-import-file" :title="artifact.fileName">{{ artifact.fileName }}</p>
    <t-alert v-if="loadFailed" theme="error" :message="t('agent.artifactDrawer.knowledgeLoadFailed')" />
    <t-alert v-else-if="!loading && !destinations.length" theme="info"
      :message="t('agent.artifactDrawer.noWritableKnowledgeBases')" />
    <t-select
      v-else
      v-model="selectedId"
      :aria-label="t('agent.artifactDrawer.knowledgeDestination')"
      :placeholder="t('manualEditor.warning.selectKnowledgeBase')"
      :options="destinations"
      :loading="loading"
      :disabled="saving"
      filterable
    />
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { downloadArtifact } from '@/api/chat'
import { useAuthStore } from '@/stores/auth'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { useOrganizationStore } from '@/stores/organization'
import { useUploadTasksStore } from '@/stores/uploadTasks'
import type { ParserEngineRule } from '@/types/knowledgeProcess'
import { kbFileTypeVerification } from '@/utils'
import { supportedKnowledgeUploadTypes } from '@/utils/fileTypeVerification'
import { permissionCanEditKB } from '@/utils/kbPermission'

const props = defineProps<{
  artifact: { sessionId: string; messageId: string; index: number; fileName: string; fileSize: number }
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const auth = useAuthStore()
const chatResources = useChatResourcesStore()
const editorResources = useEditorResourcesStore()
const organization = useOrganizationStore()
const uploadTasks = useUploadTasksStore()

interface KnowledgeBase {
  id: string
  name: string
  type?: string
  is_temporary?: boolean
  creator_id?: string
  my_permission?: string
  summary_model_id?: string
  embedding_model_id?: string
  indexing_strategy?: { vector_enabled?: boolean; keyword_enabled?: boolean }
  storage_backend_id?: string
  storage_provider_config?: { provider?: string } | null
  chunking_config?: { parser_engine_rules?: ParserEngineRule[] }
}

const selectedId = ref('')
const loading = ref(true)
const loadFailed = ref(false)
const saving = ref(false)
let active = true

const writable = computed(() => {
  const byId = new Map<string, KnowledgeBase>()
  for (const kb of chatResources.rawKnowledgeBases as KnowledgeBase[]) {
    if (kb.type === 'faq' || kb.is_temporary) continue
    const permission = organization.getKBPermission(kb.id) || kb.my_permission
    const isOwner = !!auth.user?.id && kb.creator_id === auth.user.id && auth.hasRole('contributor')
    if (permission ? permissionCanEditKB(permission) : isOwner || auth.hasRole('admin')) {
      byId.set(kb.id, kb)
    }
  }
  for (const share of organization.sharedKnowledgeBases) {
    const kb: KnowledgeBase | undefined = share.knowledge_base
    if (kb && kb.type !== 'faq' && !kb.is_temporary && permissionCanEditKB(share.permission) && !byId.has(kb.id)) {
      byId.set(kb.id, kb)
    }
  }
  return byId
})
const destinations = computed(() => [...writable.value.values()].map(kb => ({ label: kb.name, value: kb.id })))
const selectedDestination = computed(() => writable.value.get(selectedId.value))

async function loadDestinations() {
  try {
    await Promise.all([
      chatResources.ensureKnowledgeBases(),
      editorResources.ensureParserEngines().catch(() => undefined),
    ])
  } catch {
    if (active) loadFailed.value = true
  } finally {
    if (active) loading.value = false
  }
}

function close() {
  if (!active) return
  active = false
  emit('close')
}

function rejectsFile(file: Pick<File, 'name' | 'size'>, destination: KnowledgeBase) {
  const types = supportedKnowledgeUploadTypes(
    editorResources.parserEngines,
    destination.chunking_config?.parser_engine_rules,
  )
  return kbFileTypeVerification(file, false, types)
}

function isDestinationReady(destination: KnowledgeBase) {
  const strategy = destination.indexing_strategy
  const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled
  if (!destination.summary_model_id || (needsEmbedding && !destination.embedding_model_id)) {
    MessagePlugin.warning(t('knowledgeBase.notInitialized'))
    return false
  }
  if (!destination.storage_backend_id && !destination.storage_provider_config?.provider) {
    MessagePlugin.warning(t('knowledgeBase.missingStorageEngineUpload'))
    return false
  }
  return true
}

async function confirmSave() {
  const destination = selectedDestination.value
  if (!active || loading.value || loadFailed.value || saving.value || !destination) return
  const { sessionId, messageId, index, fileName, fileSize } = props.artifact
  if (!isDestinationReady(destination) || rejectsFile({ name: fileName, size: fileSize }, destination)) return

  saving.value = true
  try {
    const blob = await downloadArtifact(sessionId, messageId, index)
    if (!active) return
    const currentDestination = writable.value.get(destination.id)
    if (!currentDestination) {
      MessagePlugin.error(t('agent.artifactDrawer.noWritableKnowledgeBases'))
      return
    }

    const file = new File([blob], fileName, { type: blob.type })
    if (!isDestinationReady(currentDestination) || rejectsFile(file, currentDestination)) return
    uploadTasks.enqueue({ kbId: destination.id, kbName: currentDestination.name, uploads: [{ file }] })
    close()
  } catch {
    if (active) MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
  } finally {
    if (active) saving.value = false
  }
}

watch([() => auth.user?.id, () => auth.effectiveTenantId], close, { flush: 'sync' })
onMounted(loadDestinations)
onBeforeUnmount(() => { active = false })
</script>

<style scoped>
.artifact-import-file {
  margin: 0 0 12px;
  overflow-wrap: anywhere;
}
</style>
