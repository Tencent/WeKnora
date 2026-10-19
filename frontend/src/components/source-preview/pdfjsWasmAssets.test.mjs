import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const viewer = readFileSync(new URL('./PdfSourceViewer.vue', import.meta.url), 'utf8')
const viteConfig = readFileSync(new URL('../../../vite.config.ts', import.meta.url), 'utf8')
const renderCode = viewer.match(/async function paintPage\([\s\S]*?\n}\n\nfunction touchRendered/)?.[0] ?? ''

test('PDF preview resolves the decoder from the configured frontend base path', () => {
  assert.match(viewer, /wasmUrl\s*:\s*`\$\{import\.meta\.env\.BASE_URL\}pdfjs\/wasm\//)
})

test('production builds copy the installed pdfjs-dist wasm assets', () => {
  assert.match(viteConfig, /copyPdfJsWasm/)
  assert.match(viteConfig, /pdfJsWasmAssets/)
  assert.match(viteConfig, /require\.resolve\('pdfjs-dist\/legacy\/build\/pdf\.mjs'\)/)
  assert.match(viteConfig, /wasmOutput = resolve\(config\.root, config\.build\.outDir, 'pdfjs', 'wasm'\)/)
})

test('page-render failures are reported instead of leaving only a blank canvas', () => {
  assert.match(renderCode, /catch \(err\)[\s\S]*error\.value = t\('preview\.loadFailed'\)/)
  assert.match(renderCode, /emit\('error', error\.value\)/)
})
