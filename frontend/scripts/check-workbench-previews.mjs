// Run against the existing Vite server. Uses a private browser and intercepts
// fixture APIs only; never reads account files, logs in, or takes screenshots.
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import JSZip from 'jszip'
import * as XLSX from 'xlsx'
import { JSDOM } from 'jsdom'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const origin = process.env.PREVIEW_ORIGIN || 'http://127.0.0.1:5173'
assert.ok(process.env.PREVIEW_ARTIFACT_DIR, 'Set PREVIEW_ARTIFACT_DIR to a directory containing the generated PPTX/HTML/CSV files')
const exportsDir = pathToFileURL(resolve(process.env.PREVIEW_ARTIFACT_DIR) + '/')
const saved = await readdir(exportsDir)
const fixtures = new Map()
for (const [name, suffix] of [
  ['agent-workbench-demo.pptx', '.pptx'], ['workbench-report.html', '.html'], ['backend-summary.csv', '.csv'],
]) {
  const file = saved.find(file => file.includes(name.slice(0, -suffix.length)) && file.endsWith(suffix))
  assert.ok(file, `Missing saved Agent artifact: ${name}`)
  fixtures.set(name, await readFile(new URL(file, exportsDir)))
}
const workbook = XLSX.utils.book_new()
XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['Backend', 'Count'], ['Docker', 7]]), 'Summary')
XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['Second sheet'], [42]]), '<img src=x>')
fixtures.set('summary.xlsx', XLSX.write(workbook, { type: 'buffer', bookType: 'xlsx' }))
fixtures.set('legacy.xls', XLSX.write(workbook, { type: 'buffer', bookType: 'xls' }))
fixtures.set('probe.html', Buffer.from(`<h1>Isolated preview</h1>
  <script>parent.__previewLeak = parent.localStorage.getItem('preview-sentinel'); fetch('/api/v1/auth/me')</script>
  <img src="/api/v1/auth/me"><img src="https://preview-external.invalid/image">
  <style>@import 'https://preview-external.invalid/style'; body {background:url('/api/v1/auth/me')}</style>
  <a href="/api/v1/auth/me">blocked link</a><iframe src="/api/v1/auth/me"></iframe>`))
const oversizedPng = Buffer.alloc(24)
oversizedPng.set([0x89, 0x50, 0x4e, 0x47], 0)
oversizedPng.writeUInt32BE(12000, 16)
oversizedPng.writeUInt32BE(12000, 20)
fixtures.set('oversized.png', oversizedPng)

const dom = new JSDOM('')
const markup = await JSZip.loadAsync(fixtures.get('agent-workbench-demo.pptx'))
const markupSlide = new dom.window.DOMParser().parseFromString(await markup.file('ppt/slides/slide1.xml').async('text'), 'application/xml')
markupSlide.getElementsByTagNameNS('http://schemas.openxmlformats.org/drawingml/2006/main', 't')[0].textContent = '<img src="/api/v1/pptx-probe" onerror="globalThis.__pptxMarkup=true">'
markup.file('ppt/slides/slide1.xml', new dom.window.XMLSerializer().serializeToString(markupSlide))
fixtures.set('markup.pptx', await markup.generateAsync({ type: 'nodebuffer' }))
for (const [source, name] of [['agent-workbench-demo.pptx', 'external.pptx'], ['summary.xlsx', 'external.xlsx']]) {
  const zip = await JSZip.loadAsync(fixtures.get(source))
  const document = new dom.window.DOMParser().parseFromString(await zip.file('_rels/.rels').async('text'), 'application/xml')
  const relationship = document.createElementNS(document.documentElement.namespaceURI, 'Relationship')
  for (const [key, value] of Object.entries({ Id: 'external', Type: 'image', Target: '/api/v1/auth/me', TargetMode: 'External' })) relationship.setAttribute(key, value)
  document.documentElement.append(relationship)
  zip.file('_rels/.rels', new dom.window.XMLSerializer().serializeToString(document))
  fixtures.set(name, await zip.generateAsync({ type: 'nodebuffer' }))
}
const huge = await JSZip.loadAsync(fixtures.get('summary.xlsx'))
const worksheet = new dom.window.DOMParser().parseFromString(await huge.file('xl/worksheets/sheet1.xml').async('text'), 'application/xml')
worksheet.getElementsByTagName('dimension')[0].setAttribute('ref', 'A1:XFD1048576')
huge.file('xl/worksheets/sheet1.xml', new dom.window.XMLSerializer().serializeToString(worksheet))
fixtures.set('huge.xlsx', await huge.generateAsync({ type: 'nodebuffer' }))
dom.window.close()
const slides = Object.keys((await JSZip.loadAsync(fixtures.get('agent-workbench-demo.pptx'))).files).filter(name => /^ppt\/slides\/slide\d+\.xml$/.test(name)).length
assert.ok(slides > 1, 'Agent presentation should contain multiple pages')

const browser = await chromium.launch({ headless: true, args: ['--single-process', '--no-zygote', '--in-process-gpu'] })
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
const page = await context.newPage()
page.setDefaultTimeout(15000)
const forbidden = []
const errors = []
const downloads = []
let scenario = { mode: 'live', name: '' }
page.on('pageerror', error => errors.push(error.message))
await context.route('**/*', async route => {
  const url = new URL(route.request().url())
  if (url.origin !== origin) { forbidden.push(url.href); await route.abort(); return }
  if (url.pathname === '/__workbench-preview-check') {
    await route.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body><h1>Workbench preview check</h1><div id="app" style="height:800px"></div><script type="module" src="/scripts/fixtures/workbench-preview.ts"></script></body></html>' })
    return
  }
  if (!url.pathname.startsWith('/api/')) { await route.continue(); return }
  const base = '/api/v1/sessions/office-preview-fixture'
  const json = data => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true, data }) })
  if (url.pathname === `${base}/sandbox/files`) {
    await json({ path: '', entries: [{ name: scenario.name, path: scenario.name, type: 'file', size: fixtures.get(scenario.name).length }] })
  } else if (url.pathname === `${base}/artifacts`) {
    if (scenario.mode === 'status') await json([])
    else await json([{ index: 17, message_id: 'fixture-message', artifact_index: 2, file_name: scenario.name, file_type: `.${scenario.name.split('.').pop()}`, file_size: fixtures.get(scenario.name).length, kind: 'web_page' }])
  } else if (url.pathname === `${base}/sandbox/files/download` || url.pathname === `${base}/messages/fixture-message/artifacts/2/download`) {
    const name = url.searchParams.get('path') || scenario.name
    assert.ok(fixtures.has(name))
    downloads.push({ mode: scenario.mode, name, path: url.pathname })
    await route.fulfill({ contentType: 'application/octet-stream', body: fixtures.get(name) })
  } else if (url.pathname === `${base}/sandbox/workbench`) {
    await json({ state: 'unbound', reason: 'unbound', available: false, config_id: '', provider: '', capabilities: { files: false, terminal: false }, limits: {} })
  } else if (scenario.mode === 'status' && url.pathname.includes('sandbox-configs')) {
    await json([])
  } else if (scenario.mode === 'status' && url.pathname === `${base}/sandbox/audit`) {
    await json([])
  } else {
    forbidden.push(url.pathname)
    await route.abort()
  }
})

async function open(mode, name = '') {
  scenario = { mode, name }
  await page.goto(`${origin}/__workbench-preview-check?${new URLSearchParams(scenario)}`)
  await page.locator(mode === 'status' ? '.sandbox-workbench .workbench-status' : '#app > *').first().waitFor()
  if (mode === 'live') await page.locator('.file-name').filter({ hasText: name }).click()
  if (mode === 'artifact') await page.locator('button.artifact-name').filter({ hasText: name }).click()
  if (mode !== 'status') {
    await page.locator('.document-preview').waitFor()
    await page.locator('.preview-loading').waitFor({ state: 'hidden' })
  }
}
async function isolatedFrame() {
  const frame = page.locator('iframe.html-iframe')
  await frame.waitFor()
  assert.equal(await frame.getAttribute('sandbox'), '')
  assert.equal(await frame.getAttribute('referrerpolicy'), 'no-referrer')
  const source = await frame.getAttribute('srcdoc')
  assert.ok(source.includes("connect-src 'none'"))
  assert.ok(source.includes("script-src 'none'"))
  assert.equal(await frame.evaluate(element => element.contentDocument), null, 'Frame must have an opaque origin')
  assert.equal(await frame.contentFrame().locator('body').evaluate(() => {
    try { void window.parent.localStorage; return false } catch (error) { return error.name === 'SecurityError' }
  }), true, 'Frame cannot access parent credentials')
  return frame.contentFrame()
}

try {
  for (const mode of ['live', 'artifact']) {
    for (const name of ['agent-workbench-demo.pptx', 'workbench-report.html', 'backend-summary.csv', 'summary.xlsx', 'legacy.xls']) {
      await open(mode, name)
      assert.equal(await page.locator('.preview-error, .preview-unsupported').count(), 0, `${mode}: ${name}`)
      if (name.endsWith('.pptx')) {
        await page.locator('.pptx-preview-wrapper > div').nth(slides - 1).waitFor()
        assert.equal(await page.locator('.pptx-preview-wrapper > div').count(), slides)
        assert.ok((await page.locator('.preview-pptx').innerText()).trim().length > 30)
      } else {
        const frame = await isolatedFrame()
        await frame.locator('body').waitFor()
        assert.ok((await frame.locator('body').innerText()).trim().length > 20)
        if (/\.(xlsx|xls)$/.test(name)) {
          assert.equal(await frame.locator('table').count(), 2)
          assert.ok((await frame.locator('body').innerText()).includes('Docker'))
          assert.ok((await frame.locator('body').innerText()).includes('<img src=x>'))
          assert.equal(await frame.locator('img').count(), 0)
        }
      }
      console.log(`PASS ${mode}: ${name}${name.endsWith('.pptx') ? ` (${slides} pages)` : ''}`)
    }
    await open(mode, 'markup.pptx')
    await page.locator('.pptx-preview-wrapper > div').nth(slides - 1).waitFor()
    assert.equal(await page.locator('.pptx-preview-wrapper img').count(), 0, 'PPTX text must not create HTML elements')
    assert.equal(await page.evaluate(() => globalThis.__pptxMarkup), undefined, 'PPTX text must not execute')
    assert.ok((await page.locator('.pptx-preview-wrapper').innerText()).includes('<img src='), 'PPTX markup is literal text')
    console.log(`PASS ${mode}: literal PPTX text, no markup execution`)
    for (const name of ['external.pptx', 'external.xlsx', 'huge.xlsx']) {
      await open(mode, name)
      const message = await page.locator('.preview-error').innerText()
      assert.match(message, /Preview blocked:/)
      assert.match(message, name.startsWith('external') ? /external links or resources/ : /limits/)
      assert.equal(await page.locator('.vue-office-pptx, iframe').count(), 0)
      console.log(`PASS ${mode}: ${name} rejected before rendering`)
    }
    await open(mode, 'probe.html')
    const frame = await isolatedFrame()
    await frame.getByRole('heading', { name: 'Isolated preview' }).waitFor()
    assert.equal(await frame.locator('script, iframe, [href], [src]').count(), 0)
    assert.equal(await page.evaluate(() => window.__previewLeak), undefined)
    console.log(`PASS ${mode}: opaque HTML frame, CSP, no active content`)
    await open(mode, 'oversized.png')
    assert.match(await page.locator('.preview-error').innerText(), /dimension|pixel|尺寸|像素/i)
    assert.equal(await page.locator('.preview-image img').count(), 0)
    console.log(`PASS ${mode}: oversized image rejected before browser decode`)
  }
  for (const name of ['agent-workbench-demo.pptx', 'summary.xlsx', 'legacy.xls']) {
    await open('baseline', name)
    await page.locator(name.endsWith('.pptx') ? '.pptx-preview-wrapper > div' : '.excel-container table').first().waitFor()
    assert.equal(await page.locator('.preview-error, .preview-unsupported').count(), 0)
    console.log(`PASS baseline: ${name}`)
  }
  await open('status')
  await page.getByText('No sandbox bound', { exact: true }).first().waitFor()
  for (const label of ['Terminal', 'Files', 'Artifacts', 'Audit']) {
    const item = page.locator('.workbench-tabs .t-tabs__nav-item').filter({ hasText: label })
    await item.click()
    await page.waitForTimeout(300)
    const alignment = await item.evaluate(element => {
      const itemRect = element.getBoundingClientRect()
      const barRect = element.closest('.workbench-tabs')?.querySelector('.t-tabs__bar')?.getBoundingClientRect()
      return barRect ? {
        center: Math.abs((itemRect.left + itemRect.width / 2) - (barRect.left + barRect.width / 2)),
        width: Math.abs(itemRect.width - barRect.width),
      } : null
    })
    assert.ok(alignment && alignment.center <= 0.5 && alignment.width <= 0.5,
      `${label} tab underline is misaligned: ${JSON.stringify(alignment)}`)
  }
  assert.equal(await page.getByText('unbound', { exact: true }).count(), 0)
  assert.equal(await page.locator('.workbench-error[role="status"]').innerText(), 'No sandbox bound')
  assert.deepEqual(forbidden, [], 'No external or unexpected API requests')
  assert.deepEqual(errors, [], 'No browser exceptions')
  assert.ok(downloads.some(item => item.mode === 'artifact' && item.path.endsWith('/artifacts/2/download')))
  console.log(`PASS localized status and tab alignment; ${downloads.length} fixture downloads; no external/credential requests; no browser exceptions`)
} catch (error) {
  console.error(JSON.stringify({ scenario, forbidden, errors, preview: await page.locator('.document-preview').allTextContents(), slides: await page.locator('.pptx-preview-wrapper').evaluateAll(nodes => nodes.map(node => ({ children: node.children.length, text: node.textContent?.slice(0, 200), rect: node.getBoundingClientRect().toJSON() }))) }))
  throw error
} finally {
  await context.close()
  await browser.close()
}
