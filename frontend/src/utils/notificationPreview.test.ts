import assert from 'node:assert/strict'
import test from 'node:test'
import { notificationPreview } from './notificationPreview.ts'

test('notification excerpts preserve readable text without Markdown decoration', () => {
  assert.equal(notificationPreview('**GitHub 热榜 · 2026年10月20日**\n\n**抓取状态：** 已成功读取 [GitHub Trending](https://github.com/trending)。'),
    'GitHub 热榜 · 2026年10月20日 抓取状态： 已成功读取 GitHub Trending。')
  assert.equal(notificationPreview('# 今日简报\n- **第一项**\n- `second_item`\n\n> 备注'), '今日简报 第一项 second_item 备注')
  assert.equal(notificationPreview('**尚未结束的标题…'), '尚未结束的标题…')
  assert.equal(notificationPreview('2 * 3 = 6, snake_case'), '2 * 3 = 6, snake_case')
})

test('notification excerpts never render embedded HTML or image URLs', () => {
  assert.equal(notificationPreview('结果 <img src="https://example.com/pixel"> **完成**'), '结果 完成')
  assert.equal(notificationPreview('![示意图](https://example.com/image.png)'), '示意图')
})
