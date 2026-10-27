import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import test from 'node:test'
import { copyPdfJsWasm } from './copy-pdfjs-wasm.mjs'

const tempRoot = process.env.ISSUEHUNTER_TMP ?? tmpdir()

test('copies all installed PDF.js wasm resources to the build asset directory', (t) => {
  const root = mkdtempSync(join(tempRoot, 'pdfjs-wasm-copy-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const source = join(root, 'package-wasm')
  const output = join(root, 'dist', 'pdfjs', 'wasm')
  mkdirSync(source)
  writeFileSync(join(source, 'jbig2.wasm'), 'jbig2 decoder')
  writeFileSync(join(source, 'openjpeg.wasm'), 'openjpeg decoder')

  copyPdfJsWasm(source, output)

  assert.equal(readFileSync(join(output, 'jbig2.wasm'), 'utf8'), 'jbig2 decoder')
  assert.equal(readFileSync(join(output, 'openjpeg.wasm'), 'utf8'), 'openjpeg decoder')
})

test('fails clearly when the installed PDF.js package has no JBIG2 decoder', (t) => {
  const root = mkdtempSync(join(tempRoot, 'pdfjs-wasm-missing-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const source = join(root, 'package-wasm')
  const output = join(root, 'dist', 'pdfjs', 'wasm')
  mkdirSync(source)
  writeFileSync(join(source, 'openjpeg.wasm'), 'openjpeg decoder')

  assert.throws(() => copyPdfJsWasm(source, output), /required decoder: .*jbig2\.wasm/)
  assert.equal(existsSync(output), false, 'do not leave a seemingly complete output directory')
})
