import { unref, type MaybeRef, type Ref } from 'vue'
import { getEmbedChunkById } from '@/api/embed'
import type { CitationKnowledgeRef } from '@/utils/citationMarkdown'
import { useCitationPopover } from './useCitationPopover'

type EmbedCitationPopoverOptions = {
  getKnowledgeReferences?: () => CitationKnowledgeRef[] | null | undefined
}

export function useEmbedCitationPopover(
  rootRef: Ref<HTMLElement | null>,
  channelId: MaybeRef<string>,
  token: MaybeRef<string>,
  options?: EmbedCitationPopoverOptions,
) {
  return useCitationPopover(rootRef, {
    mode: 'embed',
    getKnowledgeReferences: options?.getKnowledgeReferences,
    getCacheScope: () => `embed:${unref(channelId)}:${unref(token)}`,
    fetchChunk: (chunkId) => getEmbedChunkById(unref(channelId), unref(token), chunkId),
    loadError: () => 'Failed to load',
  })
}
