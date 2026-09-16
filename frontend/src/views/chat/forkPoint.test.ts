import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveForkAffordance } from './forkPoint'

const checkpoint = { sandbox_id: 'sbx-1', commit_sha: 'abc', committed_at: '2026-09-10T09:00:00Z' }

test('第一条 user 消息可以分叉且不算降级', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u1'), { canFork: true, willDegrade: false })
})

test('前置 assistant 有 checkpoint 时不降级', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
    { id: 'u2', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u2'), { canFork: true, willDegrade: false })
})

test('前置 assistant 没有 checkpoint 时仍可分叉但会降级', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant' },
    { id: 'u2', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u2'), { canFork: true, willDegrade: true })
})

test('取最近的一条前置 assistant 消息，而不是更早那条', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
    { id: 'u2', role: 'user' },
    { id: 'a2', role: 'assistant' },
    { id: 'u3', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u3'), { canFork: true, willDegrade: true })
})

test('assistant 消息不能作为分叉点', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'a1'), { canFork: false, willDegrade: false })
})

test('未知消息 ID 不能分叉', () => {
  assert.deepEqual(
    resolveForkAffordance([{ id: 'u1', role: 'user' }], 'nope'),
    { canFork: false, willDegrade: false },
  )
})

test('未完成的 assistant 消息意味着本轮还在跑，不给分叉', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', is_completed: false },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u1'), { canFork: false, willDegrade: false })
})
