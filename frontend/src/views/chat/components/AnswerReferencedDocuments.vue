<script setup lang="ts">
import { computed } from 'vue'
import { useChatReferencesDrawer } from '@/composables/useChatReferencesDrawer'
import {
  buildReferenceSections,
  type KnowledgeReferenceLike,
  type ReferenceListItem,
} from '@/utils/referenceSources'

const props = defineProps<{
  references?: KnowledgeReferenceLike[]
  messageId?: string
}>()

const drawer = useChatReferencesDrawer()
const documents = computed(() =>
  buildReferenceSections(props.references?.filter(ref => ref?.knowledge_id && ref.chunk_type !== 'faq'))
    .find(section => section.id === 'documents')?.items ?? [],
)

function openDocument(item: ReferenceListItem) {
  if (!drawer || !props.references || !item.knowledgeId) return
  const chunkId = item.sourceChunkId || item.chunkId
  drawer.open({
    references: props.references,
    messageId: props.messageId,
    highlight: { key: item.key, chunkId, openSource: true },
  })
  if (!chunkId) {
    drawer.openSource({
      knowledgeId: item.knowledgeId,
      knowledgeBaseId: item.knowledgeBaseId,
      title: item.title,
      fileName: item.fileName,
    })
  }
}
</script>

<template>
  <div v-if="drawer && documents.length" class="answer-references">
    <span>{{ $t('chat.referencesDrawerTitleDocs') }}</span>
    <ul>
      <li v-for="item in documents" :key="item.key">
        <button type="button" @click.stop="openDocument(item)">
          {{ item.fileName || item.title }}
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped lang="less">
.answer-references {
  margin-top: 8px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);

  ul {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    list-style: none;
    margin: 4px 0 0;
    padding: 0;
  }

  li {
    min-width: 0;
    max-width: 100%;
  }

  button {
    border: 0;
    background: transparent;
    padding: 0;
    color: var(--td-brand-color);
    font: inherit;
    text-align: start;
    text-decoration: underline;
    overflow-wrap: anywhere;
    cursor: pointer;
  }
}
</style>
