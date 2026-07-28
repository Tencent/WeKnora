import assert from 'node:assert/strict'
import test from 'node:test'

import { formatDatabaseDriver } from './databaseDriver.ts'

test('formats supported business database drivers for display', () => {
  assert.equal(formatDatabaseDriver('mysql'), 'MySQL')
  assert.equal(formatDatabaseDriver('postgres'), 'PostgreSQL')
  assert.equal(formatDatabaseDriver('sqlite'), 'SQLite')
})

test('preserves an unknown driver instead of misidentifying it', () => {
  assert.equal(formatDatabaseDriver(' custom-db '), 'custom-db')
  assert.equal(formatDatabaseDriver(), '')
})
