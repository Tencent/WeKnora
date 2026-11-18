import { cpSync, existsSync, mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

/** Copy the installed pdfjs-dist decoder resources into the frontend build. */
export function copyPdfJsWasm(sourceDir, outputDir) {
  const jbig2Wasm = resolve(sourceDir, 'jbig2.wasm')
  if (!existsSync(jbig2Wasm)) {
    throw new Error(`PDF.js wasm resources are missing the required decoder: ${jbig2Wasm}`)
  }

  mkdirSync(outputDir, { recursive: true })
  cpSync(sourceDir, outputDir, { recursive: true })
}
