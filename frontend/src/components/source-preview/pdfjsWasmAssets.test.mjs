import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const viewer = readFileSync(new URL('./PdfSourceViewer.vue', import.meta.url), 'utf8')
const viteConfig = readFileSync(new URL('../../../vite.config.ts', import.meta.url), 'utf8')
const renderCode = viewer.match(/async function paintPage\([\s\S]*?\n}\n\nfunction touchRendered/)?.[0] ?? ''

test('PDF preview resolves CMaps, standard fonts, and decoders from the frontend base path', () => {
  assert.match(viewer, /const assetBase = `\$\{import\.meta\.env\.BASE_URL\}pdfjs\//)
  assert.match(viewer, /cMapUrl\s*:\s*`\$\{assetBase\}cmaps\//)
  assert.match(viewer, /cMapPacked\s*:\s*true/)
  assert.match(viewer, /standardFontDataUrl\s*:\s*`\$\{assetBase\}standard_fonts\//)
  assert.match(viewer, /wasmUrl\s*:\s*`\$\{assetBase\}wasm\//)
})

test('production builds copy all installed pdfjs-dist runtime assets', () => {
  assert.match(viteConfig, /copyPdfJsAssets/)
  assert.match(viteConfig, /pdfJsAssets/)
  assert.match(viteConfig, /require\.resolve\('pdfjs-dist\/legacy\/build\/pdf\.mjs'\)/)
  assert.match(viteConfig, /assetOutput = resolve\(config\.root, config\.build\.outDir, 'pdfjs'\)/)
})

test('page-render failures are reported instead of leaving only a blank canvas', () => {
  assert.match(renderCode, /catch \(err\)[\s\S]*error\.value = t\('preview\.loadFailed'\)/)
  assert.match(renderCode, /emit\('error', error\.value\)/)
})
