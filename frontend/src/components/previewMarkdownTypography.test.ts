/**
 * 知识库文档预览 Markdown 排版回归测试。
 *
 * 为什么这样断言：仓库里的测试在 `tsx --test`（Node，无 DOM）下运行，依赖里
 * 没有 jsdom/Playwright，也不适合为一个排版断言引入浏览器。这里：
 *  1. 用 `@vue/compiler-sfc` 的 `parse` + `compileStyle({ scoped, preprocessLang: 'less' })`
 *     编译组件的真实 `<style scoped lang="less">`（与 Vite 构建同一套实现，
 *     包括 `@import` 进来的 markdown.less 和 `:deep()` 的 scoped 展开）；
 *  2. 用真实的 marked 渲染管线生成正文 HTML，套进组件模板里真实的容器
 *     （`.doc-markdown-root > .md-content` / `.document-preview > .markdown-body`）；
 *  3. 用 `@vue/compiler-dom` 解析出 DOM 树，按 CSS 级联算出 `font-size` /
 *     `padding-left` / `margin-bottom` 等属性的生效值，断言关键属性。
 *
 * 这样断言的是「编译后的样式 + 真实渲染出的标签结构」，能同时抓住选择器
 * 不命中（类名/作用域写错）和样式本身太弱（标题同级、列表无缩进）两类问题。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { parse as parseSfc, compileStyle } from '@vue/compiler-sfc'

import {
  buildDomTree,
  parseCssRules,
  winningDeclaration,
  type DomNode,
} from '../test-utils/cssRuleMatcher.ts'
import { renderDocumentPreviewMarkdown } from '../utils/documentPreviewMarkdown.ts'

const SCOPE_ID = 'data-v-previewtest'
const FRONTEND_ROOT = new URL('../../', import.meta.url)
const DOC_CONTENT_SFC = new URL('src/components/doc-content.vue', FRONTEND_ROOT)
const DOCUMENT_PREVIEW_SFC = new URL('src/components/document-preview.vue', FRONTEND_ROOT)
const THEME_CSS = new URL('src/assets/theme/theme.css', FRONTEND_ROOT)

type StyleLang = 'less' | 'sass' | 'scss' | 'styl' | 'stylus'

const SAMPLE = `# 一线标题

正文段落用于确认段落间距。

## 二级标题

- 项目协同（团队版中）
- 项目分享（团队版中）

### 三级标题

> 引用块

#### 四级标题

| 能力 | 说明 |
| --- | --- |
| 列表 | 支持 |

行内 \`code\` 与代码块：

\`\`\`ts
const a = 1
\`\`\`
`

/** 编译 SFC 里所有 scoped style 块（包含 less `@import` 与 `:deep()`）。 */
function compileScopedStyle(file: URL): string {
  const filename = fileURLToPath(file)
  const { descriptor, errors } = parseSfc(readFileSync(filename, 'utf8'), { filename })
  assert.equal(errors.length, 0, `SFC parse errors in ${filename}`)
  const scoped = descriptor.styles.filter(style => style.scoped)
  assert.ok(scoped.length > 0, `expected a scoped style block in ${filename}`)
  return scoped
    .map(style =>
      compileStyle({
        source: style.content,
        filename,
        id: SCOPE_ID,
        scoped: true,
        preprocessLang: style.lang as StyleLang | undefined,
      }),
    )
    .map(result => {
      assert.equal(result.errors.length, 0, `style compile errors in ${filename}`)
      return result.code
    })
    .join('\n')
}

/** 读取 theme.css 里的 `--app-text-*` / `--app-space-*` 令牌。 */
function readThemeTokens(): Map<string, string> {
  const css = readFileSync(fileURLToPath(THEME_CSS), 'utf8')
  const tokens = new Map<string, string>()
  for (const match of css.matchAll(/--(app-(?:text|space)-[\w-]+)\s*:\s*([\d.]+px)/g)) {
    tokens.set(`--${match[1]}`, match[2])
  }
  return tokens
}

const TOKENS = readThemeTokens()

function resolvePx(value: string | undefined | null): number | null {
  if (!value) return null
  const trimmed = value.trim()
  const varMatch = trimmed.match(/^var\(\s*(--[\w-]+)\s*\)$/)
  if (varMatch) {
    const token = TOKENS.get(varMatch[1])
    return token ? Number.parseFloat(token) : null
  }
  const pxMatch = trimmed.match(/^(-?[\d.]+)px$/)
  return pxMatch ? Number.parseFloat(pxMatch[1]) : null
}

function findByTag(root: DomNode, tag: string): DomNode | null {
  for (const child of root.children) {
    if (child.tag === tag) return child
    const nested = findByTag(child, tag)
    if (nested) return nested
  }
  return null
}

function findByClass(root: DomNode, className: string): DomNode | null {
  for (const child of root.children) {
    if (child.classes.includes(className)) return child
    const nested = findByClass(child, className)
    if (nested) return nested
  }
  return null
}

function requiredDeclared(rules: ReturnType<typeof parseCssRules>, node: DomNode, property: string): string {
  const declaration = winningDeclaration(rules, node, property)
  assert.ok(
    declaration,
    `no rule declares ${property} for <${node.tag}> under .${node.classes.join('.')}`,
  )
  return declaration.value
}

function numberFor(rules: ReturnType<typeof parseCssRules>, node: DomNode, property: string): number {
  const value = requiredDeclared(rules, node, property)
  const px = resolvePx(value)
  assert.ok(px !== null, `cannot resolve ${property}: ${value}`)
  return px
}

function markdownBody(): string {
  // 两个界面用的是同一套 marked + marked-katex 结构；这里用预览管线生成
  // 真实标签（h1/h2/ul/li/p/table/blockquote/pre/code），再分别套上各自的
  // 容器与编译后的 scoped CSS。
  return renderDocumentPreviewMarkdown(SAMPLE, value => value)
}

function mergedPreviewTree(): DomNode {
  // 与 doc-content.vue 模板一致：.doc_content 下的正文根节点带 scoped 属性，
  // v-html 容器是 .md-content。
  const html = `<div class="doc_content" ${SCOPE_ID}><div class="doc-markdown-root doc-drawer-body" ${SCOPE_ID}><div class="md-content doc-merged-content" ${SCOPE_ID}>${markdownBody()}</div></div></div>`
  return buildDomTree(html)
}

function documentPreviewTree(): DomNode {
  // 与 document-preview.vue 模板一致：根 .document-preview 带 scoped 属性，
  // Markdown 渲染进 .markdown-body。
  const html = `<div class="document-preview" ${SCOPE_ID}><div class="preview-markdown"><div class="markdown-body" ${SCOPE_ID}>${markdownBody()}</div></div></div>`
  return buildDomTree(html)
}

function assertTypography(surface: string, tree: DomNode, rules: ReturnType<typeof parseCssRules>) {
  const headings = ['h1', 'h2', 'h3', 'h4'].map(tag => {
    const node = findByTag(tree, tag)
    assert.ok(node, `${surface}: rendered markup is missing <${tag}>`)
    return numberFor(rules, node, 'font-size')
  })

  for (let index = 1; index < headings.length; index += 1) {
    assert.ok(
      headings[index] < headings[index - 1],
      `${surface}: heading scale is flat (h${index}=${headings[index - 1]}px, h${index + 1}=${headings[index]}px)`,
    )
  }
  assert.notEqual(headings[0], headings[1], `${surface}: h1 and h2 must not share a font-size`)

  const ul = findByTag(tree, 'ul')
  assert.ok(ul, `${surface}: rendered markup is missing <ul>`)
  const ulIndent = numberFor(rules, ul, 'padding-left')
  assert.ok(ulIndent >= 16, `${surface}: list indent too small (${ulIndent}px)`)

  const li = findByTag(ul, 'li')
  assert.ok(li, `${surface}: rendered markup is missing <li>`)
  const liLineHeight = Number.parseFloat(requiredDeclared(rules, li, 'line-height'))
  assert.ok(liLineHeight >= 1.5, `${surface}: list line-height too tight (${liLineHeight})`)

  const paragraph = findByTag(tree, 'p')
  assert.ok(paragraph, `${surface}: rendered markup is missing <p>`)
  const paragraphGap = numberFor(rules, paragraph, 'margin-bottom')
  assert.ok(paragraphGap > 0, `${surface}: paragraphs have no bottom spacing`)

  const blockquote = findByTag(tree, 'blockquote')
  assert.ok(blockquote, `${surface}: rendered markup is missing <blockquote>`)
  const quoteBorder = numberFor(rules, blockquote, 'border-left-width')
  assert.ok(quoteBorder > 0, `${surface}: blockquote has no left border`)

  const table = findByTag(tree, 'table')
  assert.ok(table, `${surface}: rendered markup is missing <table>`)
  assert.equal(
    requiredDeclared(rules, table, 'border-collapse').trim(),
    'collapse',
    `${surface}: table borders must collapse`,
  )

  const pre = findByTag(tree, 'pre')
  assert.ok(pre, `${surface}: rendered markup is missing <pre>`)
  const preBackground = winningDeclaration(rules, pre, 'background')
  assert.ok(preBackground, `${surface}: code block has no background`)

  const inlineCode = findByTag(paragraph, 'code') ?? findByTag(tree, 'code')
  assert.ok(inlineCode, `${surface}: rendered markup is missing <code>`)
  assert.ok(
    winningDeclaration(rules, inlineCode, 'padding') ?? winningDeclaration(rules, inlineCode, 'padding-left'),
    `${surface}: inline code has no padding`,
  )

  // 正文窄栏：容器有可读的行宽上限（不是无限拉满整屏）。
  const contentRoot = findByClass(tree, 'md-content') ?? findByClass(tree, 'markdown-body')
  assert.ok(contentRoot, `${surface}: rendered markup is missing the content root`)
  const maxWidth = numberFor(rules, contentRoot, 'max-width')
  assert.ok(maxWidth >= 480 && maxWidth <= 800, `${surface}: reading column is ${maxWidth}px wide`)
}

test('merged/chunk view (.md-content) gets real Markdown typography', () => {
  const rules = parseCssRules(compileScopedStyle(DOC_CONTENT_SFC))
  assertTypography('.md-content', mergedPreviewTree(), rules)
})

test('preview view (.markdown-body) gets real Markdown typography', () => {
  const rules = parseCssRules(compileScopedStyle(DOCUMENT_PREVIEW_SFC))
  assertTypography('.markdown-body', documentPreviewTree(), rules)
})

test('scoped Markdown rules need the real container class in the DOM', () => {
  const rules = parseCssRules(compileScopedStyle(DOC_CONTENT_SFC))
  const treeWithoutClass = buildDomTree(
    `<div class="doc_content" ${SCOPE_ID}><div class="doc-markdown-root" ${SCOPE_ID}><div class="doc_content-body">${markdownBody()}</div></div></div>`,
  )
  const h1 = findByTag(treeWithoutClass, 'h1')
  assert.ok(h1)
  // 没有 .md-content 容器时，这些排版规则不应命中（正是本次要修的「选择器
  // 不命中」故障形态）。若这里开始能命中，说明规则被写成了全局标签选择器。
  assert.equal(winningDeclaration(rules, h1, 'font-size'), null)
})

test('Markdown rules no longer depend on .doc_content being the render container', () => {
  const rules = parseCssRules(compileScopedStyle(DOC_CONTENT_SFC))
  const tree = mergedPreviewTree()
  const h1 = findByTag(tree, 'h1')
  assert.ok(h1)
  const winner = winningDeclaration(rules, h1, 'font-size')
  assert.ok(winner)
  // 命中的选择器必须落在真实容器 .md-content 上，而不是抽屉外壳 .doc_content。
  assert.match(winner.selector, /\.md-content/)
  assert.doesNotMatch(winner.selector, /\.doc_content(?![\w-])/)
})
