import assert from 'node:assert/strict'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '../..')

async function loadPositionModule() {
  const server = await createServer({
    root,
    configFile: false,
    optimizeDeps: {
      entries: [],
      noDiscovery: true,
    },
    server: { middlewareMode: true, hmr: false },
    resolve: {
      alias: {
        '@': resolve(root, 'src'),
      },
    },
  })
  try {
    return await server.ssrLoadModule('/src/utils/citationFloatPosition.ts')
  } finally {
    await server.close()
  }
}

test('places citation float above anchor when viewport space below is insufficient', async () => {
  const { computeCitationFloatPosition } = await loadPositionModule()

  const position = computeCitationFloatPosition({
    anchor: { top: 720, bottom: 740, left: 420, width: 80 },
    floatSize: { width: 480, height: 260 },
    viewport: { width: 1440, height: 800, scrollX: 0, scrollY: 0 },
    offsetY: 4,
  })

  assert.equal(position.placement, 'top')
  assert.equal(position.top, 450)
})

test('keeps citation float below anchor when there is enough viewport space', async () => {
  const { computeCitationFloatPosition } = await loadPositionModule()

  const position = computeCitationFloatPosition({
    anchor: { top: 120, bottom: 140, left: 420, width: 80 },
    floatSize: { width: 480, height: 260 },
    viewport: { width: 1440, height: 800, scrollX: 0, scrollY: 0 },
    offsetY: 4,
  })

  assert.equal(position.placement, 'bottom')
  assert.equal(position.top, 150)
})
