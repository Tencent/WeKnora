import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { computed, ref } from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('./menu.vue', import.meta.url), 'utf8')
const forkStart = source.indexOf('watch(\n', source.indexOf('const pendingForkRevealId'))
const forkCode = source.slice(forkStart, source.indexOf("{ flush: 'post' }", forkStart) + "{ flush: 'post' },\n);".length)
const resizeStart = source.indexOf('let sidebarResizeStartWidth')
const resizeCode = source.slice(resizeStart, source.indexOf('</script>', resizeStart))

function forkReveal(collapsed) {
  let read, reveal, timer
  const pendingForkRevealId = ref('fork-1'), revealedSessionId = ref('')
  const container = {
    scrollTop: 0, getBoundingClientRect: () => ({ top: 0, bottom: 100 }),
    querySelectorAll: () => [{ dataset: { sessionId: 'fork-1' }, getBoundingClientRect: () => ({ top: 140, bottom: 160 }) }],
  }
  vm.runInNewContext(ts.transpile(forkCode), {
    sidebarCollapsed: computed(() => collapsed.value), pendingForkRevealId, revealedSessionId,
    currentSecondpath: ref('chat/fork-1'), filteredGroupedSessions: ref([{ items: [{ id: 'fork-1' }] }]),
    scrollContainer: ref(container), forkRevealTimer: undefined,
    watch: (getter, callback) => { read = getter; reveal = callback },
    clearTimeout: () => {}, setTimeout: callback => { timer = callback; return 1 },
  })
  return { read, reveal, pendingForkRevealId, revealedSessionId, container, finish: () => timer() }
}

test('expanded desktop sidebar scrolls to and highlights a newly forked session', () => {
  const state = forkReveal(ref(false))
  assert.equal(state.read(), 'fork-1')
  state.reveal(state.read())
  assert.equal(state.container.scrollTop, 60)
  assert.equal(state.revealedSessionId.value, 'fork-1')
  assert.equal(state.pendingForkRevealId.value, '')
  state.finish()
  assert.equal(state.revealedSessionId.value, '')
})

test('collapsed sidebar retains its pending fork until expanded', () => {
  const collapsed = ref(true), state = forkReveal(collapsed)
  assert.equal(state.read(), '')
  state.reveal(state.read())
  assert.equal(state.pendingForkRevealId.value, 'fork-1')
  collapsed.value = false
  assert.equal(state.read(), 'fork-1')
})

function resize(collapsed, width = 240) {
  const calls = [], uiStore = {
    sidebarDisplayWidth: width, sidebarWidth: width, sidebarResizing: false,
    expandSidebar: () => calls.push(['expand']), collapseSidebar: () => calls.push(['collapse']),
    resizeSidebar: value => calls.push(['resize', value]),
  }
  const handlers = vm.runInNewContext(ts.transpile(resizeCode + '\n({ startSidebarResize, resizeSidebar })'), {
    uiStore, sidebarCollapsed: computed(() => collapsed), SIDEBAR_MIN_WIDTH: 200,
  })
  handlers.startSidebarResize()
  return { ...handlers, calls, uiStore }
}

test('keyboard resizing an expanded sidebar changes its width', () => {
  const state = resize(false)
  state.resizeSidebar(16, true)
  assert.deepEqual(state.calls, [['resize', 256]])
  assert.equal(state.uiStore.sidebarResizing, true)
})

test('keyboard resize still expands collapsed sidebars and collapses at minimum width', () => {
  const collapsed = resize(true, 64)
  collapsed.resizeSidebar(16, true)
  assert.deepEqual(collapsed.calls, [['expand']])
  const minimum = resize(false, 200)
  minimum.resizeSidebar(-16, true)
  assert.deepEqual(minimum.calls, [['collapse']])
})
