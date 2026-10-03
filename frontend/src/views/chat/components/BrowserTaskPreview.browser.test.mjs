// Run with BROWSERSKILL_TEST_CHROMIUM and BROWSERSKILL_TEST_PLAYWRIGHT.
import assert from 'node:assert/strict'
import test from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const enabled = process.env.BROWSERSKILL_TEST_CHROMIUM && process.env.BROWSERSKILL_TEST_PLAYWRIGHT

test('human handoff expands, stays reachable on narrow screens, focuses the browser, and collapses after completion', { skip: !enabled }, async () => {
  const root = fileURLToPath(new URL('../../../../', import.meta.url))
  const server = await createServer({
    root, cacheDir: root+'/node_modules/.vite-browser-preview-test', configFile: false, plugins: [vue(), {
      name: 'preview-fixture',
      enforce: 'pre',
      transform(code, id) {
        if (id.split('?')[0].endsWith('/BrowserTaskPreview.vue')) return code.replaceAll("'@/utils/request'", "'/fixture-api.js'")
      },
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          if (req.url === '/fixture') {
            res.setHeader('Content-Type', 'text/html')
            res.end('<div id="app"></div><script type="module" src="/fixture-entry.js"></script>')
          } else next()
        })
      },
      resolveId(id) { if (id === '/fixture-api.js' || id === '/fixture-entry.js') return '\0'+id },
      load(id) {
        if (id === '\0/fixture-api.js') return `
          export async function get() { return {data:{...window.fixtureStatus}} }
          export async function post(_, {action}) {
            window.fixtureActions.push(action);
            return {data: action === 'preview' ? {image_base64:window.fixtureFrame || '',format:'png'} : {...window.fixtureStatus}};
          }`
        if (id === '\0/fixture-entry.js') return `
          import {createApp} from 'vue'; import {createPinia} from 'pinia';
          import {createI18n} from 'vue-i18n'; import TDesign from 'tdesign-vue-next';
          import {createRouter, createMemoryHistory} from 'vue-router';
          import 'tdesign-vue-next/es/style/index.css';
          import Preview from '/src/views/chat/components/BrowserTaskPreview.vue';
          import zh from '/src/i18n/locales/zh-CN.ts';
          window.fixtureActions=[];
          window.fixtureStatus={enabled:true,selected:true,connected:true,task_id:'fixture',needs_help:false};
          createApp(Preview,{sessionId:'fixture'}).use(createPinia()).use(createRouter({history:createMemoryHistory(),routes:[{path:'/:p(.*)*',component:{render:()=>null}}]})).use(TDesign).use(createI18n({legacy:false,locale:'zh-CN',messages:{'zh-CN':zh}})).mount('#app');`
      },
    }], resolve: {alias: {'@': root+'/src'}}, server: {host:'127.0.0.1',port:0},
  })
  const { chromium } = await import(pathToFileURL(process.env.BROWSERSKILL_TEST_PLAYWRIGHT).href)
  let browser
  try {
    await server.listen()
    browser = await chromium.launch({executablePath:process.env.BROWSERSKILL_TEST_CHROMIUM,headless:true})
    const page = await browser.newPage({viewport:{width:1100,height:800}})
    page.on('pageerror', error => console.error(error.message))
    page.on('console', message => { if (message.type() === 'error') console.error(message.text()) })
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/fixture`)
    const card = page.locator('.browser-task-preview')
    await card.waitFor()
    assert.equal(Math.round((await card.boundingBox()).width),320)
    // A new frame can change aspect ratio when the source browser is resized.
    for (const [width, height] of [[1200, 600], [600, 900]]) {
      await page.evaluate(([width, height]) => {
        const canvas = document.createElement('canvas')
        canvas.width = width; canvas.height = height
        window.fixtureFrame = canvas.toDataURL('image/png').split(',')[1]
      }, [width, height])
      await page.waitForFunction(([width, height]) => {
        const image = document.querySelector('.preview-image img')
        return image?.naturalWidth === width && image?.naturalHeight === height
      }, [width, height])
      const image = await page.locator('.preview-image img').boundingBox()
      const frame = await page.locator('.preview-image').boundingBox()
      assert.ok(Math.abs(image.width / image.height - width / height) < 0.01, 'preserve source aspect ratio')
      assert.ok(Math.abs(frame.height - image.height) <= 2, 'frame follows image without a fixed-height gap')
    }
    await page.evaluate(() => {
      window.fixtureStatus.needs_help=true
      window.fixtureStatus.help_prompt='请在浏览器中扫码登录，完成后确认。'.repeat(18)
    })
    await page.waitForSelector('.needs-help .preview-handoff')
    assert.equal(Math.round((await card.boundingBox()).width),420)
    const focus = page.locator('.preview-handoff button')
    await focus.click()
    assert.ok(await page.evaluate(() => window.fixtureActions.includes('focus')))
    await page.setViewportSize({width:320,height:480})
    await page.waitForTimeout(200)
    const bounds = await card.boundingBox()
    assert.ok(bounds.x >= 0 && bounds.x+bounds.width <= 320)
    await focus.click()
    const buttonBounds = await focus.boundingBox()
    assert.ok(buttonBounds.y >= 0 && buttonBounds.y+buttonBounds.height <= 480)
    if (process.env.BROWSERSKILL_TEST_PREVIEW_FILE) await page.screenshot({path:process.env.BROWSERSKILL_TEST_PREVIEW_FILE})
    await page.evaluate(() => { window.fixtureStatus.action='tab_borrow'; window.fixtureStatus.help_prompt='' })
    await page.waitForFunction(() => document.querySelector('.preview-handoff')?.textContent.includes('不代替借用授权'))
    assert.equal(await page.locator('.preview-handoff button').count(),0)
    assert.equal(await page.locator('.preview-image').count(),0,'do not focus the unrelated task blank tab during borrow confirmation')
    await page.evaluate(() => { window.fixtureStatus.needs_help=false; window.fixtureStatus.help_prompt='' })
    await page.waitForFunction(() => !document.querySelector('.needs-help'))
    assert.equal(await page.locator('.preview-handoff').count(),0)
    assert.equal(Math.round((await card.boundingBox()).width),280)
  } finally { await browser?.close(); await server.close() }
})
