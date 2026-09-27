// Bundled as a worker chunk with a .js name, so any static server hands it
// out with a JavaScript MIME type (module workers refuse anything else).
import pdfWorkerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?worker&url'

export type PdfJs = typeof import('pdfjs-dist/legacy/build/pdf.mjs')

let loading: Promise<PdfJs> | null = null

/**
 * Load the library once, but let each document own its worker. Reusing a port
 * while the previous loading task is being destroyed can break rapid navigation.
 */
export function loadPdfJs(): Promise<PdfJs> {
  if (!loading) {
    loading = import('pdfjs-dist/legacy/build/pdf.mjs').then((lib) => {
      lib.GlobalWorkerOptions.workerSrc = pdfWorkerUrl
      return lib
    })
    loading.catch(() => {
      loading = null
    })
  }
  return loading
}
