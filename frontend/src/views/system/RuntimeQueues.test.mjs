import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { compile, createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'

const source = readFileSync(new URL('./RuntimeQueues.vue', import.meta.url), 'utf8')
const start = source.indexOf('<div class="rq-pool-grid">')
assert.notEqual(start, -1)
const template = source.slice(start, source.indexOf('</section>', start)).trim()

for (const pending of [true, false, null, undefined]) {
  test(`worker pool restart badge with pending_restart=${pending}`, async () => {
    const app = createSSRApp({
      render: compile(template),
      setup: () => ({
        pools: [{ name: 'core', concurrency: 8, instances: 1, active: 2,
          cluster_capacity: 16, queue_count: 2, pending_restart: pending }],
        t: (key) => key,
        poolLabel: (name) => name,
        poolDescription: () => '',
        poolUtilization: () => 12.5,
      }),
    })
    app.component('t-tag', {
      setup: (_, { slots }) => () => h('span', { 'data-restart-badge': 'true' }, slots.default()),
    })
    const html = await renderToString(app)
    assert.equal(html.includes('data-restart-badge="true"'), pending === true)
    assert.ok(html.includes('2/16'), 'live capacity must remain visible')
  })
}
