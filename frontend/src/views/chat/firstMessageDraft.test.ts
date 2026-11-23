import assert from 'node:assert/strict'
import test from 'node:test'

import type { AttachmentFile } from '@/components/AttachmentUpload.vue'
import { restoreRejectedFirstMessage } from './firstMessageDraft'

test('a rejected deferred upload restores the first message draft', () => {
  const image = new File(['image'], 'diagram.png', { type: 'image/png' })
  const attachment: AttachmentFile = {
    file: new File(['document'], 'notes.docx'),
    id: 'attachment-1',
    name: 'notes.docx',
    size: 8,
    type: '.docx',
    documentId: 'temporary-document-1',
    status: 'ready' as const,
    progress: 100,
  }
  const calls: unknown[][] = []

  restoreRejectedFirstMessage(
    { restoreDraft: (...args: unknown[]) => calls.push(args) },
    'Explain the attachment',
    [image],
    [attachment],
    'file type is not supported by this agent',
  )

  assert.equal(attachment.documentId, undefined)
  assert.equal(attachment.status, 'failed')
  assert.equal(attachment.progress, 0)
  assert.equal(attachment.error, 'file type is not supported by this agent')
  assert.deepEqual(calls, [['Explain the attachment', [image], [attachment]]])
})
