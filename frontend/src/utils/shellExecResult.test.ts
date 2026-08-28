import assert from 'node:assert/strict'
import test from 'node:test'
import { buildShellExecView, previewShellCommand } from './shellExecResult.ts'

test('buildShellExecView prefers structured streams over markdown output', () => {
  const view = buildShellExecView(
    { command: 'ls', work_dir: '/workspace', exit_code: 0, stdout: 'a.txt\n', stderr: '' },
    { command: 'ignored' },
    '## Stdout\n\n```\nold\n```\n',
  )
  assert.equal(view.command, 'ls')
  assert.equal(view.workDir, '/workspace')
  assert.equal(view.exitCode, 0)
  assert.equal(view.stdout, 'a.txt\n')
})

test('buildShellExecView parses legacy markdown output when streams were stripped', () => {
  const view = buildShellExecView(
    { command: 'cat README.md', exit_code: 0 },
    null,
    [
      '=== Shell Exec (session=abc) ===',
      '',
      '**Command**: `cat README.md`',
      '**Work Dir**: /workspace',
      '**Exit Code**: 0',
      '',
      '## Stdout',
      '',
      '```',
      '# title',
      '```',
      '',
      '## Stderr',
      '',
      '```',
      'warn',
      '```',
      '',
    ].join('\n'),
  )
  assert.equal(view.command, 'cat README.md')
  assert.equal(view.stdout, '# title')
  assert.equal(view.stderr, 'warn')
})

test('previewShellCommand collapses whitespace and truncates', () => {
  assert.equal(previewShellCommand('ls   -la'), 'ls -la')
  assert.equal(previewShellCommand('abcdefghij', 8), 'abcdefg…')
})

test('buildShellExecView parses skill-script markdown and command fallback', () => {
  const view = buildShellExecView(
    {
      skill_name: 'smart-charts',
      script_path: 'scripts/cli.py',
      args: ['--x-axis', '工作项目'],
      exit_code: 1,
    },
    null,
    [
      '=== Script Execution: smart-charts/scripts/cli.py ===',
      '',
      '**Arguments**: [--x-axis 工作项目]',
      '**Exit Code**: 1',
      '',
      '## Standard Output',
      '',
      '```',
      '{"chart":{"success":false,"error":{"error":"X轴字段不存在：工作项目"}}}',
      '```',
      '',
    ].join('\n'),
  )
  assert.equal(view.command, 'smart-charts/scripts/cli.py --x-axis 工作项目')
  assert.equal(view.exitCode, 1)
  assert.match(view.stdout, /X轴字段不存在：工作项目/)
})
