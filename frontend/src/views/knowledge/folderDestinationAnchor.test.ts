import assert from 'node:assert/strict'
import test from 'node:test'
import { createFolderDestinationAnchors } from './folderDestinationAnchor.ts'
import { createUploadTasks } from '../../stores/uploadTasksCore.ts'
import type { UploadBatch } from '../../stores/uploadQueue.ts'

test('existing destination selection freezes itself and explicit root remains empty', () => {
  const state = createFolderDestinationAnchors()
  assert.deepEqual(state.pick('reports'), { path: 'reports', existingFolderAnchor: 'reports' })
  assert.deepEqual(state.pick(''), { path: '', existingFolderAnchor: '' })
})

test('nested pending folders retain the existing ancestor across picker close and reopen', () => {
  const first = createFolderDestinationAnchors()
  first.begin('reports')
  assert.deepEqual(first.created('reports/new'), { path: 'reports/new', existingFolderAnchor: 'reports' })
  const reopened = createFolderDestinationAnchors(first.snapshot())
  reopened.begin('reports/new')
  assert.deepEqual(reopened.created('reports/new/child'), {
    path: 'reports/new/child', existingFolderAnchor: 'reports',
  })
  assert.equal(reopened.pick('reports/new/child').existingFolderAnchor, 'reports')
  // No current tree lookup can turn a now-missing reports into a root anchor.
  assert.equal(reopened.pick('reports/new').existingFolderAnchor, 'reports')
})

test('root-created pending folders keep an explicit root anchor and cancellation drops creation state', () => {
  const state = createFolderDestinationAnchors()
  state.begin('')
  state.created('new')
  state.begin('new')
  assert.equal(state.created('new/child').existingFolderAnchor, '')
  state.begin('reports')
  state.cancel()
  assert.throws(() => state.created('reports/abandoned'))
})

test('ordinary folder names that match object prototype keys retain their root anchor', () => {
  const state = createFolderDestinationAnchors()
  for (const name of ['__proto__', 'constructor', 'toString']) {
    state.begin('')
    state.created(name)
    assert.equal(createFolderDestinationAnchors(state.snapshot()).pick(name).existingFolderAnchor, '')
  }
})

test('queued upload and retry preserve missing, root and non-root anchors without rebinding', async () => {
  for (const anchor of [undefined, '', 'reports']) {
    const calls: Array<{ batch: UploadBatch; reject: (reason: unknown) => void }> = []
    const tasks = createUploadTasks({
      upload: (batch) => new Promise((_resolve, reject) => calls.push({ batch, reject })),
      queryStatus: async () => [], emitListRefresh: () => {},
      owner: [() => 'user', () => 1],
      timers: { setTimer: () => ({}), clearTimer: () => {} },
    })
    tasks.enqueue({
      kbId: 'kb', kbName: 'KB', targetFolder: 'reports/new', existingFolderAnchor: anchor,
      uploads: [{ file: { name: 'unique.txt', size: 10 } as File, fileName: 'reports/new/unique.txt' }],
    })
    assert.equal(calls[0].batch.existingFolderAnchor, anchor)
    calls[0].reject({ status: 409, message: 'Folder changed; choose a destination again' })
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(tasks.items[0].transfer, 'failed')
    tasks.retryItem(tasks.items[0].id)
    assert.equal(calls[1].batch.existingFolderAnchor, anchor)
    assert.equal(calls[1].batch.targetFolder, 'reports/new')
    tasks.dismiss()
  }
})
