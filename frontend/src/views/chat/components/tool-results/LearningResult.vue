<template>
  <LearningPanel v-if="available && controller.state.error !== 'unavailable'" :controller="controller" :scope-key="scopeKey" compact
    @open-page="openPage" @open-source="openSource" />
  <span v-else class="learning-unavailable">{{ $t('learning.errors.unavailable') }}</span>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSettingsStore } from '@/stores/settings'
import { useOrganizationStore } from '@/stores/organization'
import { useLearning } from '@/composables/useLearning'
import type { LearningToolData } from '@/types/tool-results'
import LearningPanel from '@/views/knowledge/wiki/LearningPanel.vue'
const props = defineProps<{ data: LearningToolData }>()
const auth = useAuthStore()
const settings = useSettingsStore()
const organizations = useOrganizationStore()
const router = useRouter()
const available = computed(() => auth.isLoggedIn && !!props.data.knowledge_base_id
  && !settings.selectedAgentSourceTenantId
  && !organizations.sharedKnowledgeBases.some(item => item.knowledge_base?.id === props.data.knowledge_base_id))
const scopeKey = computed(() => JSON.stringify([auth.effectiveTenantId, auth.currentUserId, props.data.knowledge_base_id, available.value]))
const controller = useLearning(() => ({ kbId: props.data.knowledge_base_id, available: available.value,
  quizId: props.data.display_type === 'learning_quiz' ? props.data.quiz_id : undefined }))
function openPage(slug: string) {
  void router.push({ name: 'knowledgeBaseDetail', params: { kbId: props.data.knowledge_base_id }, query: { tab: 'wiki', slug } })
}
function openSource(knowledgeId: string) {
  void router.push({ name: 'knowledgeBaseDetail', params: { kbId: props.data.knowledge_base_id }, query: { tab: 'documents', knowledge_id: knowledgeId } })
}
</script>
<style scoped>
.learning-unavailable { color: var(--td-text-color-secondary); font-size: 12px; }
</style>
