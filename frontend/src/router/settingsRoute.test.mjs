import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const router = readFileSync(new URL('./index.ts', import.meta.url), 'utf8')
const platformShell = readFileSync(new URL('../views/platform/index.vue', import.meta.url), 'utf8')

function routeBlock(name) {
  const at = router.indexOf(`name: "${name}"`)
  assert.notEqual(at, -1, `route ${name} not found`)
  return router.slice(router.lastIndexOf('{', at), router.indexOf('}', at) + 1)
}

test('platform shell is the only place that mounts the settings modal', () => {
  // Two copies each fetch their own data, so a save in one leaves the other stale.
  assert.equal(platformShell.match(/<Settings\s*\/>/g)?.length, 1)
  assert.doesNotMatch(router, /views\/settings\/Settings\.vue/)
})

test('/platform/settings keeps a route but renders nothing into the outlet', () => {
  const block = routeBlock('settings')
  assert.match(block, /path: "settings"/)
  assert.match(block, /component: SettingsRouteOutlet/)
  assert.match(router, /const SettingsRouteOutlet = defineComponent\(\{[^}]*render: \(\) => null/)
})
