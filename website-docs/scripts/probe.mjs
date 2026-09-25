import { chromium } from 'playwright-core'
const b = await chromium.launch({ executablePath: process.env.HOME + '/Library/Caches/ms-playwright/chromium-1161/chrome-mac/Chromium.app/Contents/MacOS/Chromium' })
const q = await b.newPage()

const geom = (page) => page.evaluate(() => {
  const g = (s) => { const e = document.querySelector(s); if (!e) return null; const r = e.getBoundingClientRect(); return [Math.round(r.left), Math.round(r.right)] }
  return { logo: g('.VPNavBarTitle'), search: g('.VPNavBarSearch button, .VPNavBarSearch .DocSearch-Button'), menu: g('.VPNavBarMenu'), sidebarItem: g('.VPSidebarItem .link .text') }
})

const scanLine = async (page, label) => {
  const buf = await page.screenshot({ clip: { x: 0, y: 58, width: 1680, height: 20 } })
  const url = 'data:image/png;base64,' + buf.toString('base64')
  const hits = await q.evaluate(async (u) => {
    const img = new Image(); img.src = u; await img.decode()
    const c = document.createElement('canvas'); c.width = img.width; c.height = img.height
    const ctx = c.getContext('2d'); ctx.drawImage(img, 0, 0)
    const res = []
    for (const x of [60, 200, 400, 800, 1200, 1560, 1660]) {
      for (let y = 0; y < img.height; y++) {
        const d = ctx.getImageData(x, y, 1, 1).data
        if (d[0] < 248) res.push(`x${x} y${y + 58} ${d[0]},${d[1]},${d[2]}`)
      }
    }
    return res
  }, url)
  console.log('  line@' + label, hits.length ? hits.join(' | ') : 'none')
}

for (const w of [1440, 1680, 1920]) {
  for (const [name, path] of [['landing', '/'], ['doc    ', '/03-features/07-agent']]) {
    const p = await b.newPage({ viewport: { width: w, height: 900 }, deviceScaleFactor: 1 })
    await p.goto('http://localhost:3011' + path, { waitUntil: 'networkidle' })
    await p.waitForTimeout(800)
    console.log(w, name, JSON.stringify(await geom(p)))
    await p.evaluate(() => window.scrollTo({ top: 1200, behavior: 'instant' }))
    await p.waitForTimeout(600)
    if (w === 1680) await scanLine(p, name.trim())
    await p.close()
  }
}
const s = await b.newPage({ viewport: { width: 1680, height: 900 }, deviceScaleFactor: 2 })
for (const [name, path] of [['landing', '/'], ['doc', '/03-features/07-agent']]) {
  await s.goto('http://localhost:3011' + path, { waitUntil: 'networkidle' })
  await s.waitForTimeout(800)
  await s.evaluate(() => window.scrollTo({ top: 900, behavior: 'instant' }))
  await s.waitForTimeout(600)
  await s.screenshot({ path: `.shots/nav-${name}.png`, clip: { x: 0, y: 0, width: 1680, height: 120 } })
}
await b.close()
