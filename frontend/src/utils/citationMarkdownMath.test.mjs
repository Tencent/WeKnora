import assert from 'node:assert/strict'
import test from 'node:test'
import { joinCitationTagsToPreviousLine } from './citationMarkdown.ts'

const KB = '<kb id="11111111-1111-1111-1111-111111111111" />'
const DD = '$$'

test('keeps the blank line after a display-math block so the citation stays a separate block', () => {
  // 公式块后接引用标签。若把标签并到上一条结束的 $$ 那一行，
  // marked-katex-extension 的块级规则匹配不到结尾，整块会退化成原始 LaTeX。
  const input = ['前文', '', DD, 'E = mc^2', DD, '', KB].join('\n')

  const out = joinCitationTagsToPreviousLine(input)

  // 结束的 $$ 后面必须跟空行，标签才能独立成块
  assert.ok(
    out.includes(DD + '\n\n' + KB),
    `closing $$ must be followed by a blank line, got: ${JSON.stringify(out)}`,
  )
  // 标签不能被并到结束 $$ 那一行
  assert.ok(
    !out.includes(DD + ' ' + KB),
    `citation must not be appended to the closing $$, got: ${JSON.stringify(out)}`,
  )
})

test('still joins a citation to a normal paragraph', () => {
  // 普通段落仍要合并，否则引用标签会单独成段
  const input = ['普通段落', '', KB].join('\n')
  const out = joinCitationTagsToPreviousLine(input)
  assert.ok(out.startsWith('普通段落 '), `expected join, got: ${JSON.stringify(out)}`)
})

test('keeps fenced code delimiters on their own line', () => {
  const F = '```'
  const input = [F, 'code', F, '', KB].join('\n')
  const out = joinCitationTagsToPreviousLine(input)
  assert.ok(out.includes(F + '\n\n'), `fence must stay its own line, got: ${JSON.stringify(out)}`)
})
