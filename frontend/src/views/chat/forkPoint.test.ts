import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveForkAffordance } from './forkPoint'

test('第一条 user 消息可以分叉', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u1'), { canFork: true })
})

test('前置 assistant 无论有没有 checkpoint 都可以分叉', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant' },
    { id: 'u2', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u2'), { canFork: true })
})

test('assistant 消息可以分叉', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', is_completed: true },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'a1'), { canFork: true })
})

test('未知角色不能作为分叉点', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 's1', role: 'system' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 's1'), { canFork: false })
})

test('未知消息 ID 不能分叉', () => {
  assert.deepEqual(
    resolveForkAffordance([{ id: 'u1', role: 'user' }], 'nope'),
    { canFork: false },
  )
})

test('未完成的 assistant 消息意味着本轮还在跑，不给分叉', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', is_completed: false },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u1'), { canFork: false })
})
