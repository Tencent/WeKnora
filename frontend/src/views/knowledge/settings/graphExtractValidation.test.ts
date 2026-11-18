import assert from 'node:assert/strict'
import test from 'node:test'

import { getGraphExtractIssues, hasGraphExtractIssues, type GraphExtractConfigLike } from './graphExtractValidation'

const COMPLETE: GraphExtractConfigLike = {
  enabled: true,
  text: 'Romeo and Juliet is a tragedy by William Shakespeare.',
  tags: ['Author'],
  nodes: [
    { name: 'Romeo and Juliet', attributes: ['A tragedy'] },
    { name: 'William Shakespeare', attributes: [] },
  ],
  relations: [{ node1: 'Romeo and Juliet', node2: 'William Shakespeare', type: 'Author' }],
}

const fields = (config: GraphExtractConfigLike) => getGraphExtractIssues(config).map(issue => issue.field)

test('disabled config never reports issues', () => {
  assert.deepEqual(getGraphExtractIssues({ ...COMPLETE, enabled: false }), [])
  assert.equal(hasGraphExtractIssues(null), false)
  assert.equal(hasGraphExtractIssues(undefined), false)
})

test('complete config passes', () => {
  assert.equal(hasGraphExtractIssues(COMPLETE), false)
})

test('empty text is the exact 400 the user hit ("text cannot be empty")', () => {
  assert.deepEqual(fields({ ...COMPLETE, text: '' }), ['text'])
  assert.deepEqual(fields({ ...COMPLETE, text: '   ' }), ['text'])
})

test('empty and whitespace tags are reported separately', () => {
  assert.deepEqual(fields({ ...COMPLETE, tags: [] }), ['tags'])
  assert.deepEqual(fields({ ...COMPLETE, tags: ['Author', ''] }), ['tags'])
  assert.deepEqual(fields({ ...COMPLETE, tags: ['Author', ' '] }), ['tags'])
})

test('nodes missing or all-blank names are reported', () => {
  // 与后端一致：nodes 为空时先报 nodes，relation 引用检查被短路
  assert.deepEqual(fields({ ...COMPLETE, nodes: [] }), ['nodes'])
  assert.deepEqual(fields({ ...COMPLETE, nodes: [{ name: '' }, { name: '  ' }] }), ['nodes'])
})

test('an empty entity is rejected even when other entities are valid', () => {
  for (const name of ['', '  ', undefined]) {
    assert.deepEqual(getGraphExtractIssues({
      ...COMPLETE,
      nodes: [...COMPLETE.nodes!, { name }],
    }), [{ field: 'nodes', key: 'graphSettings.nodeNamePlaceholder' }])
  }
})

test('entity names and relation references use the same raw values as the backend', () => {
  const config: GraphExtractConfigLike = {
    ...COMPLETE,
    nodes: [{ name: ' A ' }, { name: 'A' }],
    relations: [{ node1: ' A ', node2: 'A', type: 'Author' }],
  }
  const snapshot = structuredClone(config)
  // 后端按原值识别实体；校验不能只对实体名去空格或修改待提交的数据。
  assert.deepEqual(getGraphExtractIssues(config), [])
  assert.deepEqual(config, snapshot)
})

test('a trimmed reference cannot match an untrimmed entity name', () => {
  assert.deepEqual(getGraphExtractIssues({
    ...COMPLETE,
    nodes: [{ name: ' A ' }],
    relations: [{ node1: 'A', node2: 'A', type: 'Author' }],
  }), [{ field: 'relations', key: 'graphSettings.relationNodeMismatch' }])
})

test('duplicate node names are reported (backend: duplicate node name)', () => {
  assert.deepEqual(fields({
    ...COMPLETE,
    nodes: [{ name: 'A' }, { name: 'A' }],
    relations: [{ node1: 'A', node2: 'A', type: 'Author' }],
  }), ['nodes'])
})

test('missing or partial relations are reported with distinct keys', () => {
  assert.deepEqual(getGraphExtractIssues({ ...COMPLETE, relations: [] }),
    [{ field: 'relations', key: 'graphSettings.relationsRequired' }])
  assert.deepEqual(getGraphExtractIssues({ ...COMPLETE, relations: [{ node1: 'Romeo and Juliet', node2: '', type: 'Author' }] }),
    [{ field: 'relations', key: 'graphSettings.relationIncomplete' }])
  assert.deepEqual(getGraphExtractIssues({ ...COMPLETE, relations: [{ node1: 'Nobody', node2: 'William Shakespeare', type: 'Author' }] }),
    [{ field: 'relations', key: 'graphSettings.relationNodeMismatch' }])
})

test('all four empties report all four fields (fresh toggle scenario)', () => {
  assert.deepEqual(
    fields({ enabled: true, text: '', tags: [], nodes: [], relations: [] }),
    ['text', 'tags', 'nodes', 'relations'])
})
