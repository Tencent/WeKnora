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
