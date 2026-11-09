import assert from 'node:assert/strict'
import test from 'node:test'
import { confirmKnowledgeUpload } from './uploadConfirmationTarget.ts'
import { createUploadTasks } from '../../stores/uploadTasksCore.ts'
import type { UploadBatch } from '../../stores/uploadQueue.ts'

for (const anchor of ['', 'reports']) {
  test(`global confirmation opened in A stays in A after route changes to B (${anchor || 'root'})`, async () => {
    const route = { kbId: 'kb-a', kbName: 'Knowledge A' }
    const calls: Array<{ batch: UploadBatch; resolve: (value: unknown) => void; reject: (reason: unknown) => void }> = []
    const refreshed: string[] = []
    const scheduled = new Map<unknown, () => void>()
    const tasks = createUploadTasks({
      upload: batch => new Promise((resolve, reject) => calls.push({ batch, resolve, reject })),
      queryStatus: async () => [], emitListRefresh: kbId => refreshed.push(kbId),
      owner: [() => 'user', () => 1],
      timers: {
        setTimer: fn => { const key = {}; scheduled.set(key, fn); return key },
        clearTimer: key => { scheduled.delete(key) },
      },
    })
    type Confirmation = { folder: string; anchor: string }
    let confirm!: (value: Confirmation) => void
    const pending = confirmKnowledgeUpload(route,
      () => new Promise<Confirmation>(resolve => { confirm = resolve }),
      (result, target) => tasks.enqueue({
        kbId: target.kbId, kbName: target.kbName,
        targetFolder: result.folder, existingFolderAnchor: result.anchor,
        uploads: [{ file: { name: 'unique.txt', size: 10 } as File }],
      }),
    )
    // History navigation mutates route state while the global dialog is open.
    route.kbId = 'kb-b'
    route.kbName = 'Knowledge B'
    confirm({ folder: anchor, anchor })
    await pending
    assert.equal(calls[0].batch.kbId, 'kb-a')
    assert.equal(calls[0].batch.kbName, 'Knowledge A')
    assert.equal(calls[0].batch.existingFolderAnchor, anchor)
    calls[0].reject({ status: 503, message: 'temporary failure' })
    await new Promise(resolve => setImmediate(resolve))
    tasks.retryItem(tasks.items[0].id)
    assert.equal(calls[1].batch.kbId, 'kb-a')
    assert.equal(calls[1].batch.kbName, 'Knowledge A')
    calls[1].resolve({ success: true, data: { id: 'doc-a', parse_status: 'completed' } })
    await new Promise(resolve => setImmediate(resolve))
    const scheduledCallbacks = [...scheduled.values()]
    scheduled.clear()
    scheduledCallbacks.forEach(fn => fn())
    assert.ok(refreshed.length > 0)
    assert.ok(refreshed.every(kbId => kbId === 'kb-a'))
    tasks.dismiss()
  })
}

test('cancelled confirmation never submits to a later knowledge base', async () => {
  let submitted = false
  await assert.rejects(confirmKnowledgeUpload({ kbId: 'kb-a', kbName: 'A' },
    () => Promise.reject(new Error('cancelled')),
    () => { submitted = true },
  ), /cancelled/)
  assert.equal(submitted, false)
})
