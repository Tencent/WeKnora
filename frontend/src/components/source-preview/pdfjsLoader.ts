// Bundled as a worker chunk with a .js name, so any static server hands it
// out with a JavaScript MIME type (module workers refuse anything else).
import PdfWorker from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?worker'

export type PdfJs = typeof import('pdfjs-dist/legacy/build/pdf.mjs')

let loading: Promise<PdfJs> | null = null

/**
 * Load pdf.js once per page. Every document shares one worker: pdf.js keeps
 * a caller-provided port alive when a document is destroyed.
 */
export function loadPdfJs(): Promise<PdfJs> {
  if (!loading) {
    loading = import('pdfjs-dist/legacy/build/pdf.mjs').then((lib) => {
      lib.GlobalWorkerOptions.workerPort = new PdfWorker()
      return lib
    })
    loading.catch(() => {
      loading = null
    })
  }
  return loading
}
