import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import {
  ROOT_FOLDER_FILTER,
  ROOT_FOLDER_ID,
  normalizeFolderListParentId,
  serializeFolderForBrowse,
} from '../../types/knowledgeFolder.ts'

const apiSource = readFileSync(new URL('./folders.ts', import.meta.url), 'utf8')
const contractSource = readFileSync(new URL('../../types/knowledgeFolder.ts', import.meta.url), 'utf8')

function methodBody(method: string) {
  const start = apiSource.indexOf(`  ${method}(`)
  assert.notEqual(start, -1, `missing knowledgeFolderApi.${method} method`)
  const end = apiSource.indexOf('\n  },', start)
  assert.notEqual(end, -1, `unterminated knowledgeFolderApi.${method} method`)
  return apiSource.slice(start, end)
}

test('normalizes root sentinel for the folders-list API', () => {
  assert.equal(normalizeFolderListParentId(ROOT_FOLDER_ID), ROOT_FOLDER_ID)
  assert.equal(normalizeFolderListParentId(ROOT_FOLDER_FILTER), ROOT_FOLDER_ID)
  assert.equal(normalizeFolderListParentId('folder-a'), 'folder-a')

  const rootQuery = new URLSearchParams({
    parent_id: normalizeFolderListParentId(ROOT_FOLDER_FILTER),
  }).toString()
  assert.equal(rootQuery, 'parent_id=')
  assert.ok(!rootQuery.includes(ROOT_FOLDER_FILTER))
})

test('serializes root only for direct-folder browsing', () => {
  assert.equal(ROOT_FOLDER_ID, '')
  assert.equal(ROOT_FOLDER_FILTER, '__root__')
  assert.equal(serializeFolderForBrowse('', false), '__root__')
  assert.equal(serializeFolderForBrowse('folder-a', false), 'folder-a')
  assert.equal(serializeFolderForBrowse('', true), undefined)
  assert.equal(serializeFolderForBrowse('folder-a', true), undefined)
})

test('declares the complete shared folder contract', () => {
  for (const field of [
    'id: string',
    'knowledge_base_id: string',
    'parent_id: string',
    'name: string',
    'path: string',
    'depth: number',
    'sort_order: number',
    'knowledge_count?: number | null',
    'created_at: string',
    'updated_at: string',
    'knowledgeIds: Set<string>',
    'folderIds: Set<string>',
    'folder_id?: string',
    'knowledge_ids: string[]',
    'folder_ids: string[]',
    'current_folder_id: string',
    'paths: string[]',
    'relative_path: string',
  ]) assert.ok(contractSource.includes(field), `missing contract field: ${field}`)
})

test('covers each dedicated folder API method, verb, route, and payload', () => {
  const contracts: Array<[string, string, string, string?]> = [
    ['create', 'post<KnowledgeFolder>', '/api/v1/knowledge-bases/${kbId}/folders', 'input'],
    ['list', 'get<KnowledgeFolder[]>', '/api/v1/knowledge-bases/${kbId}/folders?${query}', undefined],
    ['tree', 'get<KnowledgeFolder[]>', '/api/v1/knowledge-bases/${kbId}/folders/tree', undefined],
    ['get', 'get<KnowledgeFolder>', '/api/v1/knowledge-bases/${kbId}/folders/${folderId}', undefined],
    ['rename', 'put<KnowledgeFolder>', '/api/v1/knowledge-bases/${kbId}/folders/${folderId}', '{ name }'],
    ['move', 'post<KnowledgeFolder>', '/api/v1/knowledge-bases/${kbId}/folders/${folderId}/move', 'parent_id: parentId'],
    ['breadcrumb', 'get<KnowledgeFolder[]>', '/api/v1/knowledge-bases/${kbId}/folders/${folderId}/breadcrumb', undefined],
    ['moveKnowledge', 'put<void>', '/api/v1/knowledges/${knowledgeId}/folder', 'folder_id: folderId'],
    ['batchMove', 'post<{ success: boolean; moved_count: number }>', '/api/v1/knowledges/batch-move-folder', 'input'],
    ['batchDelete', 'post<{ success: boolean; data: { task_id: string; deleted_count: number } }>', '/api/v1/knowledge/batch-delete', 'input'],
    ['resolvePaths', 'post<ResolveFolderPathsResponse>', '/api/v1/knowledge-bases/${kbId}/folders/resolve-paths', 'input'],
  ]

  for (const [method, verb, route, payload] of contracts) {
    const body = methodBody(method)
    assert.ok(body.includes(verb), `${method} uses the expected HTTP helper`)
    assert.ok(body.includes(route), `${method} uses the complete route`)
    if (payload) assert.ok(body.includes(payload), `${method} forwards the expected payload`)
  }
})
