import assert from 'node:assert/strict'
import test from 'node:test'
import { JSDOM } from 'jsdom'

const dom = new JSDOM('', { url: 'https://app.example.com/' })
Object.defineProperty(globalThis, 'window', { value: dom.window, configurable: true })
Object.defineProperty(globalThis, 'Element', { value: dom.window.Element, configurable: true })
const { sanitizeWorkbenchPreview, WORKBENCH_PREVIEW_CSP, workbenchPreviewKind } = await import('./workbenchPreview')
const { isWorkbenchDropTarget } = await import('./sandboxWorkbench')
test.after(() => dom.window.close())

test('live HTML removes executable content, links, forms, remote sources, and nested documents', () => {
  const html = sanitizeWorkbenchPreview(`
    <meta http-equiv="refresh" content="0;url=https://evil.example">
    <base href="https://evil.example"><script>fetch('/api/v1/auth/me')</script>
    <iframe src="/api/v1/auth/me"></iframe><object data="https://evil.example"></object>
    <form action="/api/v1/tenants"><input name="x"></form>
    <a href="/api/v1/auth/me" ping="https://evil.example">link</a>
    <img src="https://evil.example/tracker" srcset="https://evil.example/2 2x" onerror="alert(1)">
    <svg><foreignObject><iframe src="https://evil.example"></iframe></foreignObject><a href="javascript:alert(1)">bad</a></svg>
    <h1>Report</h1><p>Safe contents</p>
  `)
  const result = new JSDOM(html)
  const document = result.window.document
  assert.equal(document.querySelectorAll('script, iframe, object, embed, form, input, base, foreignObject').length, 0)
  assert.equal(document.querySelectorAll('[href], [srcset], [onerror], [ping]').length, 0)
  assert.equal(document.querySelector('img')?.getAttribute('src'), null)
  assert.equal(document.querySelectorAll('meta[http-equiv]').length, 1)
  assert.equal(document.head.firstElementChild?.getAttribute('content'), WORKBENCH_PREVIEW_CSP)
  assert.equal(document.querySelector('h1')?.textContent, 'Report')
  result.window.close()
})

test('preview CSP blocks all network and auth-bearing subresources, including data images', () => {
  const directives = Object.fromEntries(WORKBENCH_PREVIEW_CSP.split(';').map(part => {
    const [name, ...value] = part.trim().split(' ')
    return [name, value.join(' ')]
  }))
  for (const name of ['default-src', 'script-src', 'connect-src', 'font-src', 'media-src', 'object-src', 'frame-src', 'base-uri', 'form-action']) assert.equal(directives[name], "'none'")
  assert.equal(directives['img-src'], "'none'")
  const html = sanitizeWorkbenchPreview('<img src="data:image/png;base64,AAAA"><img src="data:image/svg+xml,<svg onload=alert(1)></svg>">')
  const result = new JSDOM(html)
  const images = result.window.document.querySelectorAll('img')
  assert.equal(images[0].getAttribute('src'), null)
  assert.equal(images[1].getAttribute('src'), null)
  result.window.close()
})

test('restricted previews keep PPTX and spreadsheets supported and treat SVG as sanitized HTML', () => {
  const bytes = new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"><text>Hello</text></svg>')
  assert.equal(workbenchPreviewKind('svg', bytes), 'html')
  assert.equal(workbenchPreviewKind('pptx', bytes), 'pptx')
  for (const ext of ['xlsx', 'xls', 'csv', 'tsv', 'tab']) assert.equal(workbenchPreviewKind(ext, bytes), 'excel')
  for (const ext of ['pdf', 'docx', 'mp4']) assert.equal(workbenchPreviewKind(ext, bytes), 'unsupported')
  assert.equal(workbenchPreviewKind('txt', bytes), 'text')
  assert.equal(workbenchPreviewKind('html', bytes), 'html')
  assert.equal(workbenchPreviewKind('png', bytes), 'html')
})

test('Office routing is independent of content hints and still requires package validation', () => {
  const zip = new Uint8Array([0x50, 0x4b, 3, 4, 0, 0])
  assert.equal(workbenchPreviewKind('pptx', zip), 'pptx')
  assert.equal(workbenchPreviewKind('xlsx', zip), 'excel')
  assert.equal(workbenchPreviewKind('bin', zip), 'unsupported')
})

test('global drop interception excludes descendants of workbench panels and dialogs only', () => {
  const document = dom.window.document
  document.body.innerHTML = '<section data-sandbox-workbench><button><span>file</span></button></section><div class="sandbox-workbench"><header>controls</header></div><main>chat</main>'
  assert.equal(isWorkbenchDropTarget(document.querySelector('span')), true)
  assert.equal(isWorkbenchDropTarget(document.querySelector('header')), true)
  assert.equal(isWorkbenchDropTarget(document.querySelector('main')), false)
  assert.equal(isWorkbenchDropTarget(null), false)
})
