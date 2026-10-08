import assert from 'node:assert/strict'
import test from 'node:test'
import {
  ARTIFACT_CATEGORIES,
  artifactCategoryExtensions,
  artifactDateGroup,
  groupArtifactsByDate,
  parseArtifactCategory,
} from './artifactLibrary.ts'

test('"all" sends no extension filter; categories send dotted lowercase extensions', () => {
  assert.deepEqual(artifactCategoryExtensions('all'), [])
  for (const category of ARTIFACT_CATEGORIES.filter((c) => c !== 'all')) {
    const exts = artifactCategoryExtensions(category)
    assert.ok(exts.length > 0, category)
    for (const ext of exts) assert.match(ext, /^\.[a-z0-9]+$/, `${category}: ${ext}`)
  }
  assert.ok(artifactCategoryExtensions('presentation').includes('.pptx'))
})

test('categories do not overlap', () => {
  const seen = new Map()
  for (const category of ARTIFACT_CATEGORIES) {
    for (const ext of artifactCategoryExtensions(category)) {
      assert.equal(seen.get(ext), undefined, `${ext} is in both ${seen.get(ext)} and ${category}`)
      seen.set(ext, category)
    }
  }
})

test('extension lists are copies', () => {
  artifactCategoryExtensions('image').push('.exe')
  assert.ok(!artifactCategoryExtensions('image').includes('.exe'))
})

test('parseArtifactCategory falls back to all', () => {
  assert.equal(parseArtifactCategory('image'), 'image')
  assert.equal(parseArtifactCategory('bogus'), 'all')
  assert.equal(parseArtifactCategory(undefined), 'all')
  assert.equal(parseArtifactCategory(['image']), 'all')
})

test('artifactDateGroup buckets by local calendar day', () => {
  const now = new Date(2026, 8, 18, 9, 0, 0)
  assert.equal(artifactDateGroup(new Date(2026, 8, 18, 0, 5).toISOString(), now), 'today')
  assert.equal(artifactDateGroup(new Date(2026, 8, 17, 23, 59).toISOString(), now), 'yesterday')
  assert.equal(artifactDateGroup(new Date(2026, 8, 13, 12, 0).toISOString(), now), 'last7Days')
  assert.equal(artifactDateGroup(new Date(2026, 8, 1, 12, 0).toISOString(), now), 'last30Days')
  assert.equal(artifactDateGroup(new Date(2026, 5, 1, 12, 0).toISOString(), now), 'earlier')
  assert.equal(artifactDateGroup('not a date', now), 'earlier')
})

test('groupArtifactsByDate keeps order and merges consecutive items', () => {
  const now = new Date(2026, 8, 18, 9, 0, 0)
  const at = (d, h) => new Date(2026, 8, d, h).toISOString()
  const sections = groupArtifactsByDate(
    [
      { id: 1, created_at: at(18, 8) },
      { id: 2, created_at: at(18, 1) },
      { id: 3, created_at: at(17, 20) },
      { id: 4, created_at: at(2, 10) },
    ],
    now,
  )
  assert.deepEqual(
    sections.map((s) => [s.group, s.items.map((i) => i.id)]),
    [
      ['today', [1, 2]],
      ['yesterday', [3]],
      ['last30Days', [4]],
    ],
  )
})
