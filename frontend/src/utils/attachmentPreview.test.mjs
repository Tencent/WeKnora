import test from 'node:test';
import assert from 'node:assert/strict';
import {
  isPreviewableAttachment,
  resolveAttachmentFileType,
} from './attachmentPreview.ts';

test('resolveAttachmentFileType prefers explicit file_type', () => {
  assert.equal(resolveAttachmentFileType('report.PDF', '.pdf'), 'pdf');
  assert.equal(resolveAttachmentFileType('report.PDF', ''), 'pdf');
});

test('isPreviewableAttachment requires id and supported type', () => {
  assert.equal(isPreviewableAttachment({ id: 'doc-1', file_name: 'notes.md' }), true);
  assert.equal(isPreviewableAttachment({ file_name: 'notes.md' }), false);
  assert.equal(isPreviewableAttachment({ id: 'doc-1', file_name: 'archive.zip' }), false);
});
