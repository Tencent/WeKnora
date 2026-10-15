import {
  findMarkdownPage,
  indexMarkdownBlob,
  readMarkdownPage,
  type MarkdownPreviewIndex,
} from './markdownPreviewPages.ts'

let blob: Blob | null = null
let index: MarkdownPreviewIndex | null = null
self.onmessage = async ({ data }) => {
  try {
    if (data.type === 'load') {
      blob = data.blob
      index = await indexMarkdownBlob(blob!)
      const page = await readMarkdownPage(blob!, index, 0)
      self.postMessage({ id: data.id, index: 0, total: index.pages.length, page })
      return
    }
    if (!blob || !index) throw new Error('Markdown preview is not loaded')
    if (data.type === 'find') {
      const pageIndex = await findMarkdownPage(blob, index.pages, data.quote)
      const page = pageIndex >= 0 ? await readMarkdownPage(blob, index, pageIndex) : null
      self.postMessage({ id: data.id, index: pageIndex, total: index.pages.length, page })
      return
    }
    const pageIndex = data.index ?? 0
    const page = await readMarkdownPage(blob, index, pageIndex)
    self.postMessage({ id: data.id, index: pageIndex, total: index.pages.length, page })
  } catch (error) {
    self.postMessage({ id: data.id, error: error instanceof Error ? error.message : String(error) })
  }
}
