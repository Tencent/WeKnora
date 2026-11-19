import assert from 'node:assert/strict'
import test from 'node:test'

import { thinkingEqualsAnswer } from './finalAnswer.ts'

const context = 'The reviewed request includes the complete supporting documents and all of the information needed for a decision. '
const conclusion = ' The next step is to record the decision in the case history and notify the requester using the established process.'
const decision = (content: string) => `${context}${content}${conclusion}`

for (const [name, thinking, answer] of [
  ['opposite decisions', 'The request is approved.', 'The request is rejected.'],
  ['different amounts', 'The total is $1,000.', 'The total is $9,000.'],
  ['different dates', 'The deadline is 2026-10-13.', 'The deadline is 2026-10-14.'],
  ['a negated conclusion', 'The request can proceed.', 'The request cannot proceed.'],
] as const) {
  test(`keeps long reasoning with ${name} distinct from the final answer`, () => {
    assert.equal(thinkingEqualsAnswer(decision(thinking), decision(answer)), false)
    assert.equal(thinkingEqualsAnswer(decision(answer), decision(thinking)), false)
  })
}

test('deduplicates identical short and long answers', () => {
  assert.equal(thinkingEqualsAnswer('Approved.', 'Approved.'), true)
  const answer = decision('The request is approved.')
  assert.equal(thinkingEqualsAnswer(answer, answer), true)
})

test('deduplicates whitespace differences throughout a long answer', () => {
  const answer = decision('The request is approved.')
  const streamed = `\n ${answer.replaceAll(' ', '\n\t ')} \n`
  assert.equal(thinkingEqualsAnswer(streamed, answer), true)
})

test('deduplicates the existing final-answer wrappers and completed think blocks', () => {
  const answer = decision('The request is approved.')
  for (const thinking of [
    `<answer>${answer}</answer>`,
    `<final_answer><answer>${answer}</answer></final_answer>`,
    `\`\`\`final_answer\n${answer}\n\`\`\``,
    `Final Answer: ${answer}`,
    `<think>Review the evidence before deciding.</think><answer>${answer}</answer>`,
  ]) {
    assert.equal(thinkingEqualsAnswer(thinking, answer), true)
  }
})

test('does not treat empty content or different short answers as duplicates', () => {
  assert.equal(thinkingEqualsAnswer('', ''), false)
  assert.equal(thinkingEqualsAnswer(' \n\t ', 'Approved.'), false)
  assert.equal(thinkingEqualsAnswer('<think>Reviewing.</think>', ''), false)
  assert.equal(thinkingEqualsAnswer('Approved.', 'Rejected.'), false)
})
