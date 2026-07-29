import assert from 'node:assert/strict'
import test from 'node:test'

import { mergeFAQEntryPage } from './faqEntryPagination.ts'

type Entry = { id: number; standard_question: string }

test('keeps a single rendered card when an appended FAQ page overlaps the current page', () => {
  const firstPage: Entry[] = [
    { id: 20, standard_question: 'first' },
    { id: 19, standard_question: 'second' },
  ]
  const overlappingNextPage: Entry[] = [
    { id: 19, standard_question: 'second (newer response)' },
    { id: 18, standard_question: 'third' },
  ]

  assert.deepEqual(
    mergeFAQEntryPage(firstPage, overlappingNextPage).map((entry) => entry.id),
    [20, 19, 18],
  )
})
