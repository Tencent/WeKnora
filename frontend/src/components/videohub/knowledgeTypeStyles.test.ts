import assert from 'node:assert/strict'
import test from 'node:test'
import { getGraphRelationTypeLabel, getRelationDescription } from './knowledgeTypeStyles'

test('describes the five core formal relations in natural language', () => {
  assert.equal(getRelationDescription('目标知识', 'explains', true), '当前知识解释了「目标知识」')
  assert.equal(getRelationDescription('来源知识', 'explains', false), '「来源知识」解释了当前知识')
  assert.equal(getRelationDescription('互补知识', 'complements', true), '当前知识与「互补知识」是互补知识')
  assert.equal(getRelationDescription('矛盾知识', 'contradicts', false), '当前知识与「矛盾知识」存在矛盾')
  assert.equal(getRelationDescription('上位知识', 'example_of', true), '当前知识是「上位知识」的案例')
  assert.equal(getRelationDescription('案例知识', 'example_of', false), '「案例知识」是当前知识的案例')
  assert.equal(getRelationDescription('整体知识', 'part_of', true), '当前知识是「整体知识」的组成部分')
  assert.equal(getRelationDescription('组成知识', 'part_of', false), '「组成知识」是当前知识的组成部分')
})

test('uses concise graph labels for formal relation types', () => {
  assert.equal(getGraphRelationTypeLabel('explains'), '解释')
  assert.equal(getGraphRelationTypeLabel('complements'), '互补')
  assert.equal(getGraphRelationTypeLabel('contradicts'), '矛盾')
  assert.equal(getGraphRelationTypeLabel('example_of'), '案例')
  assert.equal(getGraphRelationTypeLabel('part_of'), '组成')
})
