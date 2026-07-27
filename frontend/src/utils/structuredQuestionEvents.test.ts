import assert from 'node:assert/strict'
import test from 'node:test'

import {
  historicalAskUserEvent,
  pendingSnapshotToEvent,
  reconcileStructuredQuestionEvents,
} from './structuredQuestionEvents.ts'

const required = {
  type: 'user_input_required',
  pending_id: 'pending-1',
  question: '公司如何通知你？',
  mode: 'single_choice',
  question_index: 1,
  question_total: 3,
  options: [{ id: 'written', label: '书面通知' }],
}

test('required event creates one stable unresolved card', () => {
  const result = reconcileStructuredQuestionEvents([required, required])
  assert.equal(result.length, 1)
  assert.equal(result[0].pending_id, 'pending-1')
  assert.equal(result[0].resolved, false)
})

test('resolved event updates its required card without adding a row', () => {
  const result = reconcileStructuredQuestionEvents([
    required,
    {
      type: 'user_input_resolved',
      pending_id: 'pending-1',
      status: 'answered',
      selected_options: [{ id: 'written', label: '书面通知' }],
      other_text: '',
    },
  ])
  assert.equal(result.length, 1)
  assert.equal(result[0].type, 'user_input_required')
  assert.equal(result[0].resolved, true)
  assert.equal(result[0].status, 'answered')
  assert.deepEqual(result[0].selected_options, [{ id: 'written', label: '书面通知' }])
})

test('replay preserves skipped, timed out, and canceled terminal states', () => {
  for (const status of ['skipped', 'timed_out', 'canceled']) {
    const result = reconcileStructuredQuestionEvents([
      { ...required, pending_id: status },
      { type: 'user_input_resolved', pending_id: status, status, reason: 'terminal' },
    ])
    assert.equal(result[0].resolved, true)
    assert.equal(result[0].status, status)
    assert.equal(result[0].reason, 'terminal')
  }
})

test('unmatched resolved event is not rendered as a duplicate card', () => {
  const result = reconcileStructuredQuestionEvents([
    { type: 'thinking', event_id: 'think-1' },
    { type: 'user_input_resolved', pending_id: 'missing', status: 'canceled' },
  ])
  assert.deepEqual(result, [{ type: 'thinking', event_id: 'think-1' }])
})

test('resolved typed value is hydrated into the stable card', () => {
  const result = reconcileStructuredQuestionEvents([
    { ...required, mode: 'date', field_key: 'dismissal_date' },
    { type: 'user_input_resolved', pending_id: 'pending-1', status: 'answered', value: '2026-07-22' },
  ])
  assert.equal(result[0].value, '2026-07-22')
})

test('pending snapshot restores through the same required event shape', () => {
  const event = pendingSnapshotToEvent({
    pending_id: 'pending-2', assistant_message_id: 'message-2',
    question: { question: '日期', mode: 'date', question_index: 1, question_total: 1 },
  })
  const result = reconcileStructuredQuestionEvents([event, event])
  assert.equal(result.length, 1)
  assert.equal(result[0].pending_id, 'pending-2')
})

test('historical ask_user steps rebuild a resolved question card', () => {
  const event = historicalAskUserEvent({
    id: 'call-1',
    name: 'ask_user',
    args: {
      question: '请选择案件类型', mode: 'single_choice', question_group_id: 'case-intake',
      question_index: '1', question_total: '3', allow_other: 'true', allow_skip: 'false',
      options: JSON.stringify([{ id: 'labor', label: '劳动争议', description: '辞退或欠薪' }]),
    },
    result: {
      success: true,
      output: JSON.stringify({
        status: 'answered', question_group_id: 'case-intake', question_index: 1, question_total: 3,
        selected_options: [{ id: 'labor', label: '劳动争议', description: '辞退或欠薪' }],
      }),
    },
  })

  assert.equal(event?.type, 'user_input_required')
  assert.equal(event?.pending_id, 'history-call-1')
  assert.equal(event?.resolved, true)
  assert.equal(event?.status, 'answered')
  assert.equal(event?.question_total, 3)
  assert.deepEqual(event?.options, [{ id: 'labor', label: '劳动争议', description: '辞退或欠薪' }])
  assert.deepEqual(event?.selected_options, [{ id: 'labor', label: '劳动争议', description: '辞退或欠薪' }])
})
