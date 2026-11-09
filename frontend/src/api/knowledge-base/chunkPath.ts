// Builds the chunk-list request path for a knowledge document.
//
// The backend defaults to `text` chunks when no `chunk_type` is given; other
// types (image_ocr, image_caption, ...) are returned only when requested
// explicitly. Keeping the default untouched on purpose — see issue #2857.
export function buildKnowledgeChunksPath(
  id: string,
  page: number,
  pageSize: number,
  chunkType?: string,
): string {
  const query = new URLSearchParams({
    page: String(page),
    page_size: String(pageSize),
  });
  if (chunkType) query.set('chunk_type', chunkType);
  return `/api/v1/chunks/${id}?${query.toString()}`;
}
