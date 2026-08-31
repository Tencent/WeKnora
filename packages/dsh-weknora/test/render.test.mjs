import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  appendImages,
  clipPassage,
  collectContentImages,
  collectImages,
  dropTruncatedImageTail,
  formatImageMarkdown,
  mergeImages,
  parseImageInfo,
} from '../dist/render.js'

const figure = {
  url: 'https://cdn.example.com/architecture.png',
  caption: '系统架构',
  ocr_text: 'Retriever → Rerank → LLM',
}

test('parseImageInfo reads the JSON string WeKnora stores on a chunk', () => {
  const images = parseImageInfo(JSON.stringify([figure, { caption: 'no url' }]))
  assert.deepEqual(images, [figure])
})

test('parseImageInfo also accepts an already-decoded array', () => {
  assert.deepEqual(parseImageInfo([figure]), [figure])
  assert.deepEqual(parseImageInfo('not-json'), [])
  assert.deepEqual(parseImageInfo(undefined), [])
})

test('collectContentImages finds Markdown and HTML figures', () => {
  const text = '见 ![系统架构](https://cdn.example.com/a.png) 与 <img src="https://cdn.example.com/b.png">'
  assert.deepEqual(collectContentImages(text), [
    { url: 'https://cdn.example.com/a.png', caption: '系统架构', ocr_text: '' },
    { url: 'https://cdn.example.com/b.png', caption: '', ocr_text: '' },
  ])
})

test('mergeImages deduplicates by URL and keeps the first caption and OCR', () => {
  const merged = mergeImages(
    [{ url: figure.url, caption: '系统架构', ocr_text: '' }],
    [{ url: figure.url, caption: 'ignored', ocr_text: 'Retriever → Rerank → LLM' }],
    [{ url: 'https://cdn.example.com/other.png', caption: '', ocr_text: '' }],
  )
  assert.deepEqual(merged, [
    figure,
    { url: 'https://cdn.example.com/other.png', caption: '', ocr_text: '' },
  ])
})

test('collectImages combines image_info with inline markup', () => {
  const images = collectImages({
    content: '见 ![系统架构](https://cdn.example.com/architecture.png)',
    image_info: JSON.stringify([{ url: figure.url, caption: '系统架构', ocr_text: figure.ocr_text }]),
  })
  assert.deepEqual(images, [figure])
})

test('formatImageMarkdown emits answer-ready Markdown the user can see', () => {
  const markdown = formatImageMarkdown(figure)
  assert.match(markdown, /!\[系统架构\]\(https:\/\/cdn\.example\.com\/architecture\.png\)/)
  assert.match(markdown, /\*\*Image caption:\*\* 系统架构/)
  assert.match(markdown, /\*\*Image text \(OCR\):\*\* Retriever → Rerank → LLM/)
})

test('dropTruncatedImageTail removes a Markdown image cut through its URL', () => {
  assert.equal(dropTruncatedImageTail('见 ![系统架构](https://cdn.example.com/arch'), '见')
})

test('appendImages restores figures that clipping cut out of the passage', () => {
  const passage = `前文。\n\n![系统架构](${figure.url})\n\n后文。`
  const clipped = clipPassage(passage, 6)
  assert.equal(clipped.truncated, true)
  assert.doesNotMatch(clipped.text, /cdn\.example\.com/)
  const restored = appendImages(clipped.text, [figure])
  assert.match(restored, /!\[系统架构\]\(https:\/\/cdn\.example\.com\/architecture\.png\)/)
})

test('appendImages does not duplicate a figure already in the text', () => {
  const bare = { url: figure.url, caption: '', ocr_text: '' }
  const text = `![image](${bare.url})`
  assert.equal(appendImages(text, [bare]), text)
})

test('appendImages still attaches caption and OCR to an inline figure', () => {
  const text = `![系统架构](${figure.url})`
  const out = appendImages(text, [figure])
  assert.equal(out.split(figure.url).length - 1, 1)
  assert.match(out, /\*\*Image caption:\*\* 系统架构/)
  assert.match(out, /\*\*Image text \(OCR\):\*\* Retriever → Rerank → LLM/)
})
