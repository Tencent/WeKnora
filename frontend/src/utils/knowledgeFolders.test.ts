import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  dedupeFolderPaths,
  filterFolderMentionKnowledgeBases,
  flattenFolderPages,
  folderIDFromQuery,
  mapFilesToFolderIDs,
  mergeFolderPage,
  parseRelativeFolderFiles,
  removeFolderSelection,
  restoreFolderSelections,
  runWithConcurrency,
  serializeFolderScopes,
} from './knowledgeFolders.ts';

function relativeFile(name: string, path: string) {
  const file = new File(['x'], name) as File & { webkitRelativePath: string };
  Object.defineProperty(file, 'webkitRelativePath', { value: path });
  return file;
}

describe('knowledge folder upload helpers', () => {
  it('parses, deduplicates and maps webkitRelativePath folders', () => {
    const a = relativeFile('a.md', 'project/docs/a.md');
    const b = relativeFile('b.md', 'project/docs/b.md');
    const c = relativeFile('c.md', 'project/src/c.md');
    const parsed = parseRelativeFolderFiles([a,b,c]);
    assert.deepEqual(parsed.map(item => item.segments), [['project','docs'],['project','docs'],['project','src']]);
    const paths = dedupeFolderPaths(parsed);
    assert.equal(paths.length, 2);
    const mapped = mapFilesToFolderIDs(parsed, paths.map((path,index) => ({ client_key:path.client_key, folder_id:`f-${index}` })));
    assert.equal(mapped.get(a), mapped.get(b));
    assert.notEqual(mapped.get(c), mapped.get(a));
  });

  it('caps upload concurrency', async () => {
    let active=0, peak=0;
    const tasks = Array.from({length:12},(_,index) => async () => { active++;peak=Math.max(peak,active);await Promise.resolve();active--;return index; });
    const results = await runWithConcurrency(tasks,4);
    assert.equal(results.every(result => result.status==='fulfilled'), true);
    assert.ok(peak <= 4);
  });
});

describe('knowledge folder selection helpers', () => {
  const folders = [
    { id: 'folder-a', name: 'A', kbId: 'kb-1' },
    { id: 'folder-a', name: 'A duplicate', kbId: 'kb-1' },
    { id: 'folder-b', name: 'B', kbId: 'kb-1' },
    { id: 'folder-a', name: 'A in another KB', kbId: 'kb-2' },
  ];

  it('serializes folder scopes by KB and deduplicates folder IDs', () => {
    assert.deepEqual(serializeFolderScopes(folders), [
      { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-b'] },
      { knowledge_base_id: 'kb-2', folder_ids: ['folder-a'] },
    ]);
    assert.equal(serializeFolderScopes([]), undefined);
  });

  it('removes only the requested folder selection', () => {
    assert.deepEqual(removeFolderSelection(folders, 'folder-a', 'kb-1'), [folders[2], folders[3]]);
  });

  it('restores named mentions and falls back to folder scopes', () => {
    assert.deepEqual(restoreFolderSelections([
      { id: 'folder-a', name: 'Plans', type: 'folder', kb_id: 'kb-1', kb_name: 'KB' },
      { id: 'folder-a', name: 'Plans', type: 'folder', kb_id: 'kb-1', kb_name: 'KB' },
      { id: 'tag-a', name: 'Tag', type: 'tag', kb_id: 'kb-1' },
    ], [{ knowledge_base_id: 'ignored', folder_ids: ['ignored'] }]), [
      { id: 'folder-a', name: 'Plans', kbId: 'kb-1', kbName: 'KB' },
      { id: 'ignored', name: 'ignored', kbId: 'ignored' },
    ]);
    assert.deepEqual(restoreFolderSelections([], [
      { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-a', 'folder-b'] },
    ]), [
      { id: 'folder-a', name: 'folder-a', kbId: 'kb-1' },
      { id: 'folder-b', name: 'folder-b', kbId: 'kb-1' },
    ]);
  });

  it('filters folder mention KBs to the agent scope and excludes FAQ KBs', () => {
    const available = [
      { id: 'kb-1', type: 'document' },
      { id: 'kb-2', type: 'document' },
      { id: 'kb-faq', type: 'faq' },
    ];
    assert.deepEqual(filterFolderMentionKnowledgeBases(available, new Set(['kb-2', 'kb-faq'])), [available[1]]);
  });
});

describe('knowledge folder tree helpers', () => {
  it('merges lazy pages without duplicating folders and flattens expanded paths', () => {
    const first = mergeFolderPage(undefined, [
      { id: 'a', name: 'A' },
      { id: 'b', name: 'B old' },
    ], 1, 3);
    const root = mergeFolderPage(first, [
      { id: 'b', name: 'B' },
      { id: 'c', name: 'C' },
    ], 2, 3);
    const child = mergeFolderPage(undefined, [{ id: 'a-1', name: 'A1' }], 1, 2);
    const rows = flattenFolderPages(new Map([['', root], ['a', child]]), new Set(['a']));
    assert.deepEqual(rows, [
      { kind: 'folder', folder: { id: 'a', name: 'A' }, depth: 0 },
      { kind: 'folder', folder: { id: 'a-1', name: 'A1' }, depth: 1 },
      { kind: 'more', parentId: 'a', depth: 1 },
      { kind: 'folder', folder: { id: 'b', name: 'B' }, depth: 0 },
      { kind: 'folder', folder: { id: 'c', name: 'C' }, depth: 0 },
    ]);
  });

  it('normalizes URL folder_id values for root restoration', () => {
    assert.equal(folderIDFromQuery('folder-a'), 'folder-a');
    assert.equal(folderIDFromQuery('root'), '');
    assert.equal(folderIDFromQuery(['folder-a']), '');
    assert.equal(folderIDFromQuery(undefined), '');
  });
});
