import assert from 'node:assert/strict'
import test from 'node:test'
import type { ArtifactMeta } from '../api/chat'
import type { AuditLog } from '../api/tenant/audit-log'
import { artifactTarget, auditDetails, validWorkbenchPath, workbenchFileLimit, workbenchSnapshotKey, workbenchSocketUrl } from './sandboxWorkbench'

test('WebSocket URL follows API subpaths and rejects credentials, queries, and other endpoints', () => {
  const path = '/api/v1/sandbox-terminal'
  assert.equal(workbenchSocketUrl('', path, 'https://example.com/chat/s'), 'wss://example.com/api/v1/sandbox-terminal')
  assert.equal(workbenchSocketUrl('/app/weknora/', path, 'http://localhost:15173/chat/s'), 'ws://localhost:15173/app/weknora/api/v1/sandbox-terminal')
  assert.equal(workbenchSocketUrl('https://api.example.com/sub/', path, 'https://ui.example.com'), 'wss://api.example.com/sub/api/v1/sandbox-terminal')
  for (const invalid of ['https://evil.example/api/v1/sandbox-terminal', '//evil.example', `${path}?ticket=secret`, `${path}#auth`]) {
    assert.throws(() => workbenchSocketUrl('', invalid, 'https://example.com'))
  }
  assert.throws(() => workbenchSocketUrl('https://user:password@example.com', path, 'https://example.com'))
})

test('relative destination validation rejects traversal, control characters and absolute paths', () => {
  assert.equal(validWorkbenchPath('', true), true)
  for (const path of ['', '/tmp/file', '../file', 'dir/../file', 'dir//file', 'dir/./file', 'dir\\file', 'a\0b', 'a\nb']) assert.equal(validWorkbenchPath(path), false, path)
  for (const path of ['file.txt', '.env', 'reports/new name.txt', '报告/a.csv']) assert.equal(validWorkbenchPath(path), true, path)
  assert.equal(workbenchFileLimit(99_000_000), 8 * 1024 * 1024)
  assert.equal(workbenchFileLimit(1024), 1024)
})

test('snapshots distinguish same-path same-size replacements by revision', () => {
  assert.notEqual(workbenchSnapshotKey('report.txt', 1), workbenchSnapshotKey('report.txt', 2))
  assert.notEqual(workbenchSnapshotKey('a', 1), workbenchSnapshotKey('b', 1))
})

test('session artifacts require both additive identifiers and do not use the flattened index', () => {
  assert.equal(artifactTarget({ index: 17 } as ArtifactMeta), null)
  assert.equal(artifactTarget({ message_id: 'message', index: 17 } as ArtifactMeta), null)
  assert.deepEqual(artifactTarget({ index: 17, message_id: 'message', artifact_index: 0 } as ArtifactMeta), { messageId: 'message', index: 0 })
})

test('audit details accept JSON objects and historical JSON strings, preserving zero exit codes', () => {
  const details = { command: 'true', duration_ms: 0, exit_code: 0 }
  assert.deepEqual(auditDetails({ details }), details)
  assert.deepEqual(auditDetails({ details: JSON.stringify(details) }), details)
  assert.deepEqual(auditDetails({ details: 'invalid json' }), {})
})
