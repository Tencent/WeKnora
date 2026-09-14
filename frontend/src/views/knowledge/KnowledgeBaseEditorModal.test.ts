import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const source = readFileSync(new URL('./KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')

test('新建知识库先展示名称和描述，再展示类型与索引设置', () => {
  const basicStart = source.indexOf("currentSection === 'basic'")
  const modelsStart = source.indexOf("currentSection === 'models'", basicStart)
  assert.ok(basicStart >= 0 && modelsStart > basicStart, '找不到知识库基本信息区')

  const basicSection = source.slice(basicStart, modelsStart)
  const name = basicSection.indexOf('v-model="formData.name"')
  const description = basicSection.indexOf('v-model="formData.description"')
  const type = basicSection.indexOf('v-model="formData.type"')
  const indexing = basicSection.indexOf('data-guide="kb-create-indexing"')

  assert.ok(name >= 0, '基本信息区应包含知识库名称')
  assert.ok(description > name, '知识库描述应紧随名称展示')
  assert.ok(type > description, '知识库类型应在名称和描述之后展示')
  assert.ok(indexing > type, '索引设置应在知识库类型之后展示')
})
