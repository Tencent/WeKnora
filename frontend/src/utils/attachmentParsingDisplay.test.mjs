import assert from 'node:assert/strict'
import test from 'node:test'

import {
  getAttachmentParsingSummaryHtml,
  resolveAttachmentParsingCounts,
} from './attachmentParsingDisplay.ts'

const t = (key, params) => {
  if (key === 'agentStream.attachmentParsing.parsedSummary') {
    return `已解析 ${params?.count} 个附件`
  }
  if (key === 'agentStream.attachmentParsing.parsedWithSkipped') {
    return `已解析 ${params?.parsed} 个附件，${params?.skipped} 个未完成已跳过`
  }
  if (key === 'agentStream.attachmentParsing.noneReady') {
    return '没有可用的已解析附件'
  }
  return key
}

test('resolveAttachmentParsingCounts prefers structured tool_data', () => {
  assert.deepEqual(
    resolveAttachmentParsingCounts({
      tool_data: { parsed_count: 2, skipped_count: 1 },
    }),
    { parsed: 2, skipped: 1 },
  )
})

test('resolveAttachmentParsingCounts falls back to legacy output text', () => {
  assert.deepEqual(
    resolveAttachmentParsingCounts({
      output: '已解析 3 个附件，1 个未完成已跳过',
    }),
    { parsed: 3, skipped: 1 },
  )
})

test('getAttachmentParsingSummaryHtml renders parsed count', () => {
  const html = getAttachmentParsingSummaryHtml(t, {
    success: true,
    tool_data: { parsed_count: 1, skipped_count: 0 },
  })
  assert.equal(html, '已解析 <strong>1</strong> 个附件')
})

test('getAttachmentParsingSummaryHtml renders skipped count', () => {
  const html = getAttachmentParsingSummaryHtml(t, {
    success: true,
    tool_data: { parsed_count: 2, skipped_count: 1 },
  })
  assert.equal(html, '已解析 <strong>2</strong> 个附件，<strong>1</strong> 个未完成已跳过')
})
