import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { after, before, test } from 'node:test'
import { createSSRApp, h, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { compileStyleAsync, parse } from '@vue/compiler-sfc'
import vue from '@vitejs/plugin-vue'
import { createServer, type ViteDevServer } from 'vite'
import { getAgentToolIconName } from '../utils/agent-tool-icons'

const iconNames = [
  'lightbulb', 'ai-search', 'bookmark', 'search', 'internet', 'data-search', 'file-search',
  'file', 'file-paste', 'task', 'folder', 'edit', 'attach', 'terminal', 'code', 'chat',
  'agent', 'browser', 'jump', 'compaction', 'ellipsis', 'check-circle', 'play-circle',
  'time', 'minus-circle', 'close-circle', 'help-circle', 'secured', 'lock-on',
]
const filename = fileURLToPath(new URL('./ChatTimelineIcon.vue', import.meta.url))
const source = readFileSync(filename, 'utf8')
let server: ViteDevServer
let Icon: Component
let css: string

before(async () => {
  server = await createServer({
    configFile: false, optimizeDeps: { noDiscovery: true, entries: [] }, plugins: [vue()],
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  Icon = (await server.ssrLoadModule('/src/components/ChatTimelineIcon.vue')).default
  const { descriptor } = parse(source)
  const result = await compileStyleAsync({
    filename, source: descriptor.styles[0].content,
    id: 'data-v-motion', scoped: true, preprocessLang: 'less',
  })
  assert.deepEqual(result.errors, [])
  css = result.code
})
after(async () => { await server?.close() })

async function render(name: string, active?: boolean) {
  return renderToString(createSSRApp({ render: () => h(Icon, { name, active }) }))
}

test('active icons keep their viewport and stroke, while completed and historical records contain no animation', async () => {
  for (const name of iconNames) {
    const active = await render(name, true)
    const complete = await render(name, false)
    const historical = await render(name)
    assert.match(active, /class="chat-timeline-icon is-active"/)
    assert.doesNotMatch(complete, /is-active|icon-motion|<animate/i)
    assert.doesNotMatch(historical, /is-active|icon-motion|<animate/i)
    for (const html of [active, complete, historical]) {
      assert.match(html, /width="18" height="18" viewbox="0 0 24 24"/i)
      assert.match(html, /stroke-width="1.65"/)
      assert.match(html, /aria-hidden="true"/)
    }
  }
})

test('every tool icon family has a semantic motion, including the unknown-tool fallback', async () => {
  const tools = [
    'thinking', 'search_knowledge', 'wiki_search', 'web_search', 'web_fetch', 'read_document',
    'todo_write', 'attachment_parsing', 'call_mcp_tool', 'shell_exec', 'list_sandbox_files',
    'read_file', 'write_sandbox_file', 'execute_skill_script', 'spawn_agent', 'local_browser',
    'open_page', 'unknown_tool',
  ]
  const names = new Set([...tools.map(tool => getAgentToolIconName(tool)), ...iconNames])
  for (const name of names) {
    const html = await render(name, true)
    assert.ok(html.includes(`data-icon="${name}"`), `${name} must use its own glyph`)
    assert.match(html, /<animate(?:Transform)?\s/i, `${name} must have a motion on its working part`)
  }
  assert.match(await render('unknown_tool', true), /data-icon="file-paste"/)
  for (const name of ['chevron-right', 'chevron-down']) {
    assert.doesNotMatch(await render(name, true), /<animate/i, 'disclosure controls remain still')
  }
})

test('reduced motion keeps the exact static glyph and movement is opt-in', async () => {
  const mediaStart = css.indexOf('@media (prefers-reduced-motion: no-preference)')
  assert.ok(mediaStart >= 0)
  assert.match(css.slice(0, mediaStart), /\.icon-motion[^{}]*\{\s*display: none/)
  assert.doesNotMatch(css.slice(0, mediaStart), /icon-still/)
  assert.match(css.slice(mediaStart), /\.is-active[^{}]*> .icon-still[^{}]*\{\s*display: none/)
  assert.match(css.slice(mediaStart), /\.icon-motion[^{}]*\{\s*display: inline/)
  assert.doesNotMatch(css, /\bscale(?:X|Y)?\(|\bfilter:|will-change/)
  for (const name of iconNames) {
    const still = (html: string) => html.match(/<g class="icon-still"[^>]*>([\s\S]*?)<\/g>/)?.[1]
    const active = still(await render(name, true))
    assert.ok(active)
    assert.equal(active, still(await render(name, false)), name)
  }
})

test('native timelines have valid spline segments, compatible path morphs and a seamless repeat', async () => {
  // Validate the emitted SVG, not just the descriptors: malformed SMIL silently
  // stops animating in browsers even when the Vue template compiles successfully.
  for (const name of iconNames) {
    const html = await render(name, true)
    for (const [tag] of html.matchAll(/<animate(?:Transform)?\s[^>]*>/gi)) {
      const attr = (key: string) => tag.match(new RegExp(`\\b${key}="([^"]*)"`, 'i'))?.[1]
      const values = attr('values')!.split(';')
      const times = attr('keyTimes')!.split(';').map(Number)
      const splines = attr('keySplines')!.split(';')
      assert.equal(values.length, times.length, name)
      assert.equal(splines.length, times.length - 1, name)
      assert.equal(times[0], 0)
      assert.equal(times.at(-1), 1)
      assert.ok(times.every((time, index) => Number.isFinite(time) && (!index || time > times[index - 1])))
      for (const spline of splines) {
        const points = spline.split(' ').map(Number)
        assert.equal(points.length, 4)
        assert.ok(points.every(point => Number.isFinite(point) && point >= 0 && point <= 1))
      }
      if (attr('type') === 'rotate') {
        const first = values[0].split(' ').map(Number)
        const last = values.at(-1)!.split(' ').map(Number)
        assert.equal(Math.abs(last[0] - first[0]) % 360, 0, `${name} must not jump on repeat`)
        assert.deepEqual(first.slice(1), last.slice(1))
      } else {
        assert.equal(values[0], values.at(-1), `${name} must not jump on repeat`)
      }
      assert.equal(attr('calcMode'), 'spline')
      assert.equal(attr('repeatCount'), 'indefinite')
      assert.equal(attr('dur'), '2.8s')
      if (attr('attributeName') === 'd') {
        const commands = (path: string) => path.match(/[a-z]/gi)
        for (const value of values) assert.deepEqual(commands(value), commands(values[0]), name)
      } else if (attr('attributeName') === 'transform') {
        assert.ok(['translate', 'rotate'].includes(attr('type')!), 'strokes must never be scaled')
      } else {
        assert.ok(['opacity', 'stroke-dashoffset'].includes(attr('attributeName')!))
        assert.ok(values.every(value => Number(value) >= 0 && Number(value) <= 1))
      }
    }
  }
})

test('drawing strokes have a normalized length and reset only after fading out', async () => {
  for (const name of ['lightbulb', 'file', 'task', 'terminal', 'chat', 'check-circle']) {
    const html = await render(name, true)
    const strokes = [...html.matchAll(/<path[^>]*pathLength="1"[^>]*>[\s\S]*?<\/path>/gi)]
    assert.ok(strokes.length, name)
    for (const [path] of strokes) {
      assert.match(path, /stroke-dasharray="1"/)
      assert.match(path, /stroke-dashoffset="1"/)
      assert.match(path, /opacity="0"/)
      const values = (attribute: string) => path.match(new RegExp(`attributeName="${attribute}" values="([^"]+)"`, 'i'))![1].split(';')
      const offset = values('stroke-dashoffset')
      const opacity = values('opacity')
      for (let index = 1; index < offset.length; index++) {
        if (offset[index] > offset[index - 1]) {
          assert.equal(opacity[index - 1], '0')
          assert.equal(opacity[index], '0')
        }
      }
    }
  }
})

test('both agent timeline branches gate tools and reasoning on the live segment', () => {
  const agent = readFileSync(new URL('../views/chat/components/AgentStreamDisplay.vue', import.meta.url), 'utf8')
  assert.equal((agent.match(/:name="getToolIconName\(event.tool_name\)" :active="!isSegmentDone && event.pending"/g) || []).length, 2)
  assert.equal((agent.match(/:active="!isSegmentDone && isThinkingActive\(event.event_id\)"/g) || []).length, 2)
  assert.equal((agent.match(/:active="!isSegmentDone && \(event.pending \|\| isThinkingActive\(event.tool_call_id\)\)"/g) || []).length, 2)
  assert.equal((agent.match(/:motion-paused="isSegmentDone"/g) || []).length, 2)
  const rag = readFileSync(new URL('../views/chat/components/RagPipelineProgress.vue', import.meta.url), 'utf8')
  assert.equal((rag.match(/:active="step.pending && !hasAnswer && !session\?\.is_completed"/g) || []).length, 2)
  assert.equal((rag.match(/:active="thinkingActive"/g) || []).length, 2)
  assert.match(rag, /:active="!waitStepStalled"/)
})
