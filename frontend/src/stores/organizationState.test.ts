import assert from 'node:assert/strict'
import test from 'node:test'
import {
  applyOrganizationResourceDelta,
  upsertById
} from './organizationState.ts'

test('upsert adds a newly joined organization to the front', () => {
  const result = upsertById(
    [{ id: 'existing', name: 'Existing' }],
    { id: 'joined', name: 'Joined' }
  )

  assert.deepEqual(result.map(organization => organization.id), ['joined', 'existing'])
})

test('upsert replaces edited organization data without duplicating the card', () => {
  const result = upsertById(
    [{ id: 'space-1', name: 'Old name' }],
    { id: 'space-1', name: 'New name' }
  )

  assert.deepEqual(result, [{ id: 'space-1', name: 'New name' }])
})

test('knowledge base sharing updates card and resource counts immediately', () => {
  const result = applyOrganizationResourceDelta(
    [{ id: 'space-1', share_count: 2 }],
    {
      knowledge_bases: { by_organization: { 'space-1': 2 } },
      agents: { by_organization: { 'space-1': 1 } }
    },
    'space-1',
    'knowledge_bases',
    1
  )

  assert.equal(result.organizations[0].share_count, 3)
  assert.equal(result.resourceCounts?.knowledge_bases.by_organization['space-1'], 3)
})

test('resource counts never become negative when a share is removed', () => {
  const result = applyOrganizationResourceDelta(
    [{ id: 'space-1', agent_share_count: 0 }],
    {
      knowledge_bases: { by_organization: {} },
      agents: { by_organization: { 'space-1': 0 } }
    },
    'space-1',
    'agents',
    -1
  )

  assert.equal(result.organizations[0].agent_share_count, 0)
  assert.equal(result.resourceCounts?.agents.by_organization['space-1'], 0)
})
