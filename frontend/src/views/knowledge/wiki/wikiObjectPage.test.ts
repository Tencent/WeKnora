import assert from 'node:assert/strict'
import test from 'node:test'

import {
  getWikiObjectType,
  isVisibleWikiContentPage,
} from './wikiObjectPage.ts'

const objectPages = [
  ['entity', '汪天凡'],
  ['concept', '网络效应'],
  ['methodology', '早期项目判断框架'],
  ['case', 'Looki 投资案例'],
  ['insight', '应用层仍被低估'],
] as const

const ordinaryIndexPage = {
  page_type: 'index',
  slug: 'video/example',
  content: '---\ntype: knowledge_base\nbusiness_type: index\npage_type: index\n---\n\n# 视频索引',
}

test('recognizes all five P4 object types from index frontmatter', () => {
  for (const [type, title] of objectPages) {
    const page = {
      page_type: 'index',
      slug: `knowledge-object/${type}/example`,
      content: `---\npage_type: index\ntype: ${type}\nprimary_type: ${type}\n---\n\n# ${title}`,
    }
    assert.equal(getWikiObjectType(page), type)
    assert.equal(isVisibleWikiContentPage(page), true)
  }
})

test('hides ordinary index pages and rejects inconsistent object metadata', () => {
  assert.equal(getWikiObjectType(ordinaryIndexPage), null)
  assert.equal(isVisibleWikiContentPage(ordinaryIndexPage), false)

  const inconsistentPage = {
    page_type: 'index',
    content: '---\ntype: case\nprimary_type: insight\n---\n\n# 不一致',
  }
  assert.equal(getWikiObjectType(inconsistentPage), null)
  assert.equal(isVisibleWikiContentPage(inconsistentPage), false)
})
