import assert from 'node:assert/strict';
import test from 'node:test';

import { resolveKnowledgeDownloadFileName } from './knowledgeDownloadFileName.ts';

test('prefers the original filename over the extensionless display name', () => {
  assert.equal(resolveKnowledgeDownloadFileName({
    id: 'knowledge-1',
    original_file_name: 'quarterly-report.pdf',
    file_name: 'quarterly-report',
    type: 'file',
  }), 'quarterly-report.pdf');
});

test('falls back to the backend filename when no original filename is present', () => {
  assert.equal(resolveKnowledgeDownloadFileName({
    id: 'knowledge-2',
    file_name: 'notes.txt',
    type: 'file',
  }), 'notes.txt');
});

test('adds the markdown extension to manual documents exactly once', () => {
  assert.equal(resolveKnowledgeDownloadFileName({
    id: 'knowledge-3',
    title: 'meeting-notes',
    type: 'manual',
  }), 'meeting-notes.md');
  assert.equal(resolveKnowledgeDownloadFileName({
    id: 'knowledge-4',
    title: 'meeting-notes.md',
    type: 'manual',
  }), 'meeting-notes.md');
});
