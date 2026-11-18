import assert from 'node:assert/strict'
import test from 'node:test'
import { showLiveShellOutput } from './shellProgress.ts'

test('async shell progress stays visible after the tool call has returned', () => {
  assert.equal(showLiveShellOutput({
    tool_name: 'shell_exec_async',
    pending: false,
    command_output: { done: false },
  }), true)
  assert.equal(showLiveShellOutput({
    tool_name: 'shell_exec',
    pending: false,
    command_output: { done: false },
  }), false, '同步命令只在工具还在跑时显示进度')
  assert.equal(showLiveShellOutput({
    tool_name: 'shell_exec_async',
    pending: false,
    command_output: { done: true },
  }), false)
})
