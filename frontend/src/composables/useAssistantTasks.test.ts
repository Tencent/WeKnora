import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import * as vue from 'vue'
import * as tasks from '../utils/assistantTasks.ts'
import { fixture, flush } from '../test-utils/vueFixture.ts'

const source = readFileSync(new URL('./useAssistantTasks.ts', import.meta.url), 'utf8')
const event = {user_id:'u',tenant_id:1,session_id:'s',change:{operation:'knowledge.move',area:'knowledge'},tasks:[{kind:'move',id:'t'}]}
function setup(t: any, get: (...args:any[]) => Promise<unknown>) {
  const f = fixture(t)
  const owner = vue.ref('u:1'), session = vue.ref('s')
  const changed = t.mock.fn()
  const module = f.evaluate(source, {vue, '@/utils/request':{get}, '@/utils/assistantTasks':tasks,
    '@/utils/assistantDataChange': {emitAssistantDataChange:changed}})
  let tracker: any
  const mounted = f.mount({setup(){tracker = module.useAssistantTasks(owner, session)}})
  const emit = () => f.window.dispatchEvent(new CustomEvent(tasks.ASSISTANT_TASK_EVENT,{detail:event}))
  return {f, owner, session, changed, emit, mounted, get tracker(){return tracker}}
}
test('deduplicates, polls across conversation changes, refreshes on completion', async t => {
  let calls = 0
  const f = setup(t, async () => ({data:{status:++calls === 1 ? 'pending' : 'completed'}}))
  f.emit(); f.emit(); await flush()
  assert.equal(calls,1)
  f.session.value = 'other'; await flush()
  assert.equal(f.tracker.tasks.value.length,0)
  t.mock.timers.tick(3000); await flush()
  assert.equal(f.changed.mock.callCount(),1)
  f.session.value = 's'; await flush()
  assert.equal(f.tracker.tasks.value[0].state,'completed')
})
test('workspace switch invalidates an in-flight result and cannot refresh the new workspace', async t => {
  let resolve!: (x: unknown) => void
  const f = setup(t, () => new Promise(r => {resolve=r}))
  f.emit(); f.owner.value = 'u:2'
  resolve({data:{status:'completed'}}); await flush()
  assert.equal(f.changed.mock.callCount(),0)
  assert.equal(f.tracker.tasks.value.length,0)
})
test('status read failure is unknown; retry never resubmits a write', async t => {
  let calls = 0
  const f = setup(t, async (path) => {assert.ok(path.endsWith('/progress/t')); calls++; throw Error('404')})
  f.emit(); await flush()
  assert.equal(f.tracker.tasks.value[0].state,'unknown')
  assert.equal(f.changed.mock.callCount(),0)
  f.tracker.retry(f.tracker.tasks.value[0]); await flush()
  assert.equal(calls,2)
})
