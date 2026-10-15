import assert from 'node:assert/strict'
import test from 'node:test'
import { tasksFromEvent, taskStatusPath, taskState } from './assistantTasks.ts'

const event = {user_id: 'u', tenant_id: 1, session_id: 's', started_at: 100, change: {operation: 'knowledge.move'}, tasks: [{kind: 'move', id: 'task'}]}
test('task descriptors preserve session and reject foreign owners and arbitrary adapters', () => {
  assert.equal(tasksFromEvent(event, 'u:1', 200)[0].sessionId, 's')
  assert.equal(tasksFromEvent(event, 'u:1', 200)[0].startedAt, 100)
  assert.deepEqual(tasksFromEvent(event, 'u:2'), [])
  assert.deepEqual(tasksFromEvent(event, 'other:1'), [])
  assert.deepEqual(tasksFromEvent({...event, tasks:[{kind:'http',id:'https://evil'}]}, 'u:1'), [])
  assert.equal(taskStatusPath({kind:'move',id:'../other?x=1'}), '/api/v1/knowledge/move/progress/..%2Fother%3Fx%3D1')
  assert.equal(taskStatusPath({kind:'unknown',id:'task'}), undefined)
})
test('accepted, inaccessible, failed and partial tasks never become completed', () => {
  assert.equal(taskState('move', {data:{status:'pending'}}), 'running')
  assert.equal(taskState('evaluation', {data:{task:{status:0}}}), 'running')
  assert.equal(taskState('evaluation', {data:{task:{status:2}}}), 'completed')
  assert.equal(taskState('evaluation', {data:{task:{status:3}}}), 'failed')
  assert.equal(taskState('parse', {data:{parse_status:'failed'}}), 'failed')
  assert.equal(taskState('parse', {data:{parse_status:'completed'}}), 'completed')
  assert.equal(taskState('sync', {status:'partial'}), 'partial')
  assert.equal(taskState('move', {data:{status:'completed',failed:1}}), 'partial')
  assert.equal(taskState('faq_import', {data:{status:'completed',partial_failed_count:1}}), 'partial')
  for (const response of [null, {}, {success:false}, {$httpStatus:404}, {data:{status:'new-unknown-state'}}]) {
    assert.equal(taskState('move', response), 'unknown')
  }
})
