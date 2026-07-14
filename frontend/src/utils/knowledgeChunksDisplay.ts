import type { ComposerTranslation } from 'vue-i18n'

import type { KnowledgeChunksListData } from '@/types/tool-results'

export function getKnowledgeChunksSummaryHtml(
  t: ComposerTranslation,
  toolData: KnowledgeChunksListData | null | undefined,
): string {
  if (!toolData || toolData.fetched_chunks === undefined) {
    return ''
  }

  const parts: string[] = [
    t('agentStream.knowledgeChunksList.chunkRange', {
      fetched: `<strong>${toolData.fetched_chunks ?? 0}</strong>`,
      total: `<strong>${toolData.total_chunks ?? '?'}</strong>`,
    }),
  ]

  const total = Number(toolData.total_chunks ?? 0)
  const pageSize = Number(toolData.page_size ?? 0)
  if (total > pageSize && pageSize > 0) {
    parts.push(
      t('agentStream.knowledgeChunksList.page', {
        page: toolData.page ?? 1,
        pageSize,
      }),
    )
  }

  return parts.join(' · ')
}
