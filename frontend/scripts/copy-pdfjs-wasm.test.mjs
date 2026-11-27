import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import test from 'node:test'
import { copyPdfJsAssets } from './copy-pdfjs-wasm.mjs'

const tempRoot = process.env.ISSUEHUNTER_TMP ?? tmpdir()

test('copies all required PDF.js runtime resources to the build asset directory', (t) => {
  const root = mkdtempSync(join(tempRoot, 'pdfjs-assets-copy-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const source = join(root, 'pdfjs-dist')
  const output = join(root, 'dist', 'pdfjs')
  mkdirSync(join(source, 'cmaps'), { recursive: true })
  mkdirSync(join(source, 'standard_fonts'), { recursive: true })
  mkdirSync(join(source, 'wasm'), { recursive: true })
  writeFileSync(join(source, 'cmaps', 'Adobe-GB1-UCS2.bcmap'), 'Chinese CMap')
  writeFileSync(join(source, 'standard_fonts', 'LiberationSans-Regular.ttf'), 'standard font')
  writeFileSync(join(source, 'wasm', 'jbig2.wasm'), 'jbig2 decoder')
  writeFileSync(join(source, 'wasm', 'openjpeg.wasm'), 'openjpeg decoder')

  copyPdfJsAssets(source, output)

  assert.equal(readFileSync(join(output, 'cmaps', 'Adobe-GB1-UCS2.bcmap'), 'utf8'), 'Chinese CMap')
  assert.equal(readFileSync(join(output, 'standard_fonts', 'LiberationSans-Regular.ttf'), 'utf8'), 'standard font')
  assert.equal(readFileSync(join(output, 'wasm', 'jbig2.wasm'), 'utf8'), 'jbig2 decoder')
  assert.equal(readFileSync(join(output, 'wasm', 'openjpeg.wasm'), 'utf8'), 'openjpeg decoder')
})

test('fails clearly when the installed PDF.js package has incomplete runtime resources', (t) => {
  const root = mkdtempSync(join(tempRoot, 'pdfjs-assets-missing-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const source = join(root, 'pdfjs-dist')
  const output = join(root, 'dist', 'pdfjs')
  mkdirSync(join(source, 'cmaps'), { recursive: true })

  assert.throws(() => copyPdfJsAssets(source, output), /required asset: .*Adobe-GB1-UCS2\.bcmap/)
  assert.equal(existsSync(output), false, 'do not leave a seemingly complete output directory')
})
