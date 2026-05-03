// Chunker debug / preview API. Single endpoint that runs the adaptive
// chunker over a sample text without touching DB or embeddings. Used by
// the KB editor debug panel.

import { post } from '../../utils/request'
import type {
  PreviewChunkingRequest,
  PreviewChunkingResponse
} from '../../types/chunker'

export function previewChunking(
  body: PreviewChunkingRequest
): Promise<{ success: boolean; data: PreviewChunkingResponse }> {
  return post('/api/v1/chunker/preview', body)
}
