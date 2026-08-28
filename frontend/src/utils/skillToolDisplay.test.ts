import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatToolTitleWithDetail,
  getEventSkillName,
  getReadSkillTarget,
  getSandboxToolPath,
  sandboxFileListItems,
  skillFileListItems,
  skillScriptTitleCommand,
} from './skillToolDisplay.ts'

test('formatToolTitleWithDetail quotes a skill or path detail', () => {
  assert.equal(formatToolTitleWithDetail('读取技能', 'pdf-processing'), '读取技能：「pdf-processing」')
  assert.equal(formatToolTitleWithDetail('读取技能', '  '), '读取技能')
})

test('getReadSkillTarget prefers skill/file from tool_data over arguments', () => {
  assert.equal(
    getReadSkillTarget({
      arguments: { skill_name: 'old', file_path: 'SKILL.md' },
      tool_data: { skill_name: 'pdf-processing', file_path: 'scripts/run.py' },
    }),
    'pdf-processing/scripts/run.py',
  )
  assert.equal(
    getReadSkillTarget({ arguments: { skill_name: 'brandkit' } }),
    'brandkit',
  )
})

test('getEventSkillName parses JSON-encoded arguments', () => {
  assert.equal(
    getEventSkillName({ arguments: '{"skill_name":"smart-charts"}' }),
    'smart-charts',
  )
})

test('skillScriptTitleCommand drops a duplicated skill prefix', () => {
  assert.equal(
    skillScriptTitleCommand('pdf-processing', 'pdf-processing/scripts/run.py --fast'),
    'scripts/run.py --fast',
  )
  assert.equal(skillScriptTitleCommand('pdf-processing', 'ls'), 'ls')
})

test('skillFileListItems skips SKILL.md and marks scripts', () => {
  const items = skillFileListItems({
    files: ['SKILL.md', 'scripts/run.py', 'docs/guide.md', ''],
  })
  assert.deepEqual(
    items.map((item) => ({ path: item.path, isScript: item.isScript })),
    [
      { path: 'scripts/run.py', isScript: true },
      { path: 'docs/guide.md', isScript: false },
    ],
  )
})

test('sandboxFileListItems uses paths relative to the artifact root', () => {
  const items = sandboxFileListItems({
    root: '/workspace/output',
    path: '/workspace/output',
    entries: [
      { name: 'report.html', path: '/workspace/output/report.html', size: 12, modified_at: '2026-08-28T01:02:03Z' },
      { name: 'chart.png', path: '/workspace/output/charts/chart.png', size: 2048 },
    ],
  })
  assert.equal(items[0].name, 'report.html')
  assert.equal(items[1].name, 'charts/chart.png')
  assert.equal(items[0].size, 12)
})

test('getSandboxToolPath reads the listed directory', () => {
  assert.equal(
    getSandboxToolPath({ tool_data: { path: '/workspace/output/charts' } }),
    '/workspace/output/charts',
  )
})
