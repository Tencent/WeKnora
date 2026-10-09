import assert from 'node:assert/strict'
import test from 'node:test'

import {
  approvalEventFromRecord,
  buildHostResolveBody,
  hostApprovalTitleKey,
  isHostApprovalEvent,
  sessionRuleLabels,
} from './hostApproval'

test('a stored approval becomes a resolved timeline event', () => {
  const event = approvalEventFromRecord({
    pending_id: 'pending-1',
    kind: 'host_command',
    tool_call_id: 'call-1',
    host: { reason: 'delete', command: 'rm a.txt' },
    resolved: true,
    approved: false,
    reason: 'user rejected',
    scope: 'once',
  })
  assert.equal(event.type, 'tool_approval_required')
  assert.equal(event.resolved, true)
  assert.equal(event.approved, false)
  assert.equal(event.resolve_reason, 'user rejected')
  assert.equal(event.resolve_scope, 'once')
  assert.equal(isHostApprovalEvent(event), true)
})

test('only host_command events use the host card', () => {
  assert.equal(isHostApprovalEvent({ kind: 'host_command' }), true)
  assert.equal(isHostApprovalEvent({ kind: '' }), false)
  assert.equal(isHostApprovalEvent({}), false)
  assert.equal(isHostApprovalEvent({ host: { reason: 'delete' } }), true)
})

test('titles follow the approval reason', () => {
  assert.equal(hostApprovalTitleKey('delete'), 'agentStream.hostApproval.titleDelete')
  assert.equal(hostApprovalTitleKey('dangerous'), 'agentStream.hostApproval.titleDangerous')
  assert.equal(hostApprovalTitleKey('sandbox_denied'), 'agentStream.hostApproval.titleDenied')
  assert.equal(hostApprovalTitleKey('unknown'), 'agentStream.hostApproval.titleGeneric')
})

test('session scope is only sent when the request allows it', () => {
  assert.deepEqual(buildHostResolveBody('approve', { scope: 'session', allowSession: true }), {
    decision: 'approve',
    scope: 'session',
  })
  assert.deepEqual(buildHostResolveBody('approve', { scope: 'session', allowSession: false }), {
    decision: 'approve',
    scope: 'once',
  })
})

test('access can only narrow a write proposal to read', () => {
  assert.deepEqual(
    buildHostResolveBody('approve', { scope: 'once', access: 'read', proposedAccess: 'write' }),
    { decision: 'approve', scope: 'once', access: 'read' },
  )
  assert.deepEqual(
    buildHostResolveBody('approve', { scope: 'once', access: 'write', proposedAccess: 'read' }),
    { decision: 'approve', scope: 'once' },
  )
})

test('the card lists the delete rules a session approval remembers', () => {
  assert.deepEqual(
    sessionRuleLabels({ reason: 'delete', allow_session: true, session_rules: ['rm -f -r', 'rmdir'] }),
    ['rm -f -r …', 'rmdir …'],
  )
  assert.deepEqual(sessionRuleLabels({ reason: 'delete', allow_session: false, session_rules: ['rm'] }), [])
  assert.deepEqual(sessionRuleLabels({ reason: 'sandbox_denied', allow_session: true }), [])
})

test('reject carries only the reason', () => {
  assert.deepEqual(buildHostResolveBody('reject', { reason: 'no', scope: 'session', allowSession: true }), {
    decision: 'reject',
    reason: 'no',
  })
})
