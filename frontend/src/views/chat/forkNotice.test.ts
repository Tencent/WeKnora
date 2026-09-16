import assert from 'node:assert/strict'
import test from 'node:test'
import { forkDegradeMessage, forkSuccessMessage } from './forkNotice'

test('每个降级原因码都有对应文案', () => {
  assert.equal(
    forkDegradeMessage('NO_CHECKPOINT'),
    '未能复制沙箱环境（该轮未创建检查点），已为分支创建全新环境',
  )
  assert.equal(
    forkDegradeMessage('SANDBOX_REPLACED'),
    '原会话中途更换过沙箱，未能复制环境，已创建全新环境',
  )
  assert.equal(
    forkDegradeMessage('SANDBOX_GONE'),
    '原会话沙箱已回收，未能复制环境，已创建全新环境',
  )
  assert.equal(
    forkDegradeMessage('SNAPSHOT_UNSUPPORTED'),
    '当前沙箱后端不支持环境复制，已创建全新环境',
  )
})

test('未知原因码退化为通用文案而不是显示原始码', () => {
  assert.equal(forkDegradeMessage('SOMETHING_NEW'), '未能复制沙箱环境，已创建全新环境')
})

test('空原因码返回空串，调用方据此不显示横幅', () => {
  assert.equal(forkDegradeMessage(''), '')
})

test('成功分叉提示说明环境来自分叉时刻而不是分叉点', () => {
  const text = forkSuccessMessage()
  assert.ok(text.includes('分叉操作时'))
  assert.ok(text.includes('不是分叉点'))
  assert.notEqual(text, forkDegradeMessage('NO_CHECKPOINT'))
})
