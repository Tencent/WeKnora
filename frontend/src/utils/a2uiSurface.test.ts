import assert from 'node:assert/strict'
import test from 'node:test'
import type { Tokens } from 'marked'

import { markdownDomPurifyConfig } from './markdownDomPurify.ts'
import {
  A2UI_BASIC_CATALOG_ID,
  a2uiPendingCardHtml,
  a2uiPlaceholderHtml,
  cleanA2uiPayload,
  createA2uiCodeRenderer,
  createSurfaceAwareCodeRenderer,
  decodeA2uiFrames,
  decodeA2uiPayloadText,
  encodeA2uiFrames,
  markA2uiTagBlocks,
  parseA2uiFrames,
} from './a2uiSurface.ts'

// Minimal structural type for the frame shapes the tests touch, so we can read
// properties off the (else-unknown) decoded payload without fabricating the
// whole v0.9 message union.
interface A2uiFrame {
  version: string
  createSurface?: { surfaceId: string; catalogId: string }
  updateComponents?: {
    surfaceId: string
    components: Array<{ id: string; component: string; text?: string; child?: string; children?: string[] }>
  }
}

// The frame body carries a CJK label ("知道了") to pin down "the encoding must be UTF-8 safe" — a naive
// btoa(json) throws RangeError straight away on non-Latin-1 characters, and any regression here goes red.
const CJK_FRAMES = JSON.stringify([
  { version: 'v0.9', createSurface: { surfaceId: 's1', catalogId: A2UI_BASIC_CATALOG_ID } },
  { version: 'v0.9', updateComponents: { surfaceId: 's1', components: [
    { id: 'root', component: 'Card', child: 'col1' },
    { id: 'col1', component: 'Column', children: ['lbl', 'go'] },
    { id: 'lbl', component: 'Text', text: '知道了' },
    { id: 'go', component: 'Button', child: 'lbl' },
  ] } },
])

// marked hands a code renderer a full `Tokens.Code`. The util only reads
// lang/text, but its slot is typed `Renderer['code']`, so the tests build a
// real token instead of a two-field literal.
function codeToken(text: string, lang: string): Tokens.Code {
  return { type: 'code', raw: text, lang, text }
}

test('encode/decode round-trips a frames array containing CJK labels', () => {
  const payload = decodeA2uiFrames(encodeA2uiFrames(CJK_FRAMES)) as A2uiFrame[]
  assert.equal(payload.length, 2)
  assert.equal(payload[0].createSurface?.surfaceId, 's1')
  // Literal, not A2UI_BASIC_CATALOG_ID: the constant is consumed by provideA2UI
  // in the same module that defines it, so comparing the two would hold no
  // matter what the constant said. This is the id the backend stamps into every
  // createSurface frame; a2ui-vue matches catalogs by exact id and throws
  // "Catalog not found" on a mismatch, so a silent rewrite of it must go red.
  assert.equal(
    payload[0].createSurface?.catalogId,
    'https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json',
  )
  const comps = payload[1].updateComponents?.components ?? []
  assert.equal(comps.find((c) => c.id === 'lbl')?.text, '知道了')
})

test('a single message object is accepted, because the server accepts it too', () => {
  // ExtractA2UIMessages (internal/types/agent_card.go) tolerates a model that
  // emits one message OBJECT rather than an array, and its executor will happily
  // run a button out of that object. If the renderer refused it, the button
  // would exist server-side while the user saw nothing — so both sides must
  // accept it. Same fixture shape as the array leg below, one frame only.
  const single = JSON.stringify({
    version: 'v0.9',
    createSurface: { surfaceId: 's7', catalogId: A2UI_BASIC_CATALOG_ID },
  })
  assert.deepEqual(parseA2uiFrames(single), [
    { version: 'v0.9', createSurface: { surfaceId: 's7', catalogId: A2UI_BASIC_CATALOG_ID } },
  ])

  // The shapes neither side can turn into a surface: a scalar is refused, an
  // empty array stays empty (nothing to mount, no throw).
  assert.throws(() => parseA2uiFrames('"just a string"'), /not a JSON object or array/)
  assert.throws(() => parseA2uiFrames('42'), /not a JSON object or array/)
  assert.deepEqual(parseA2uiFrames('[]'), [])
})

test('a broken fence still yields its source text for the code-block fallback', () => {
  // The mounter shows the fence body instead of swallowing it, which only works
  // if base64 decoding succeeds independently of JSON parsing.
  const broken = '{"version":"v0.9","createSurface":{'
  const b64 = encodeA2uiFrames(broken)
  assert.equal(decodeA2uiPayloadText(b64), broken)
  assert.throws(() => parseA2uiFrames(decodeA2uiPayloadText(b64)), SyntaxError)
})

test('decode rejects malformed base64', () => {
  assert.throws(() => decodeA2uiFrames('not!!base64'))
})

test('placeholder carries its payload as base64 that the sanitizer cannot strip', () => {
  const html = a2uiPlaceholderHtml(CJK_FRAMES)
  assert.match(html, /^<div class="chat-a2ui-surface" data-a2ui-frames="[A-Za-z0-9+\/=]+" data-a2ui-state="pending"><\/div>$/)
  // The payload contains only base64 characters (no double quotes / angle brackets), so v-html + DOMPurify keep it verbatim.
  const m = html.match(/data-a2ui-frames="([^"]*)"/)
  if (!m) throw new Error('payload attribute missing from placeholder')
  assert.match(m[1], /^[A-Za-z0-9+\/=]+$/)

  // The attribute survives DOMPurify for exactly two reasons, pinned here
  // against the config the chat/agent markdown path actually uses
  // (`markdownDomPurifyConfig` is what `sanitizeMarkdownHTML` passes in). This
  // is config verification, not a pipeline run: the repo test setup has no real
  // DOM (test-utils/vueFixture hands components a vm sandbox with a fake
  // `document`), so DOMPurify degrades to a no-op there and a sanitize() call
  // would prove nothing.
  const cfg = markdownDomPurifyConfig as unknown as Record<string, unknown>
  // 1. The data-* pass-through (purify source: `if (ALLOW_DATA_ATTR &&
  //    DATA_ATTR.test(lcName)) ;`) has an empty body — it keeps the attribute
  //    and skips the value checks. SAFE_FOR_TEMPLATES is the only knob that
  //    turns it off.
  assert.equal(cfg.SAFE_FOR_TEMPLATES, undefined, 'SAFE_FOR_TEMPLATES would set ALLOW_DATA_ATTR=false')
  // 2. FORBID_ATTR outranks everything, even the data-* branch, so a data-*
  //    entry there would kill the payload. Positive anchor first: the list must
  //    be populated (event handlers only), otherwise "no data-* entry" is just
  //    an empty array nodding along.
  const forbids = (cfg.FORBID_ATTR ?? []) as readonly string[]
  assert.ok(forbids.includes('onclick'), 'FORBID_ATTR is expected to list event handlers')
  assert.deepEqual(forbids.filter((a) => /^data-/.test(String(a))), [])
  // 3. USE_PROFILES wholesale-replaces ALLOWED_TAGS/ALLOWED_ATTR (purify source
  //    re-creates both sets from the profiles alone), so this config's
  //    ALLOWED_ATTR list never reaches the check. That is why data-a2ui-frames
  //    needs no entry there and why it must not be added: the repo's own
  //    `data-mermaid` is already listed and already survives on this same
  //    data-* branch, not on the list.
  assert.ok(cfg.USE_PROFILES, 'markdown config relies on USE_PROFILES, which replaces ALLOWED_ATTR')
})

test('a2ui fence becomes a placeholder; every other fence falls through', () => {
  const seen: unknown[] = []
  // The fallback returns an opaque constant on purpose: an expectation built
  // from the same template as the stub (FALLBACK:${lang}:${text}) would hold by
  // construction and prove nothing. Passthrough is asserted against the
  // sentinel, argument forwarding against `seen`.
  const fallback = (token: Tokens.Code) => {
    seen.push(token)
    return 'PLAIN-CODE-BLOCK'
  }
  const renderer = createA2uiCodeRenderer(fallback)
  const a2uiOut = renderer(codeToken(CJK_FRAMES, 'a2ui'))
  assert.match(a2uiOut, /^<div class="chat-a2ui-surface"/)
  assert.deepEqual(seen, [])
  const mermaidOut = renderer(codeToken('graph TD;', 'mermaid'))
  assert.equal(mermaidOut, 'PLAIN-CODE-BLOCK')
  assert.deepEqual(seen, [codeToken('graph TD;', 'mermaid')])
})

test('the embedded plane keeps the fence a code block, the normal plane gets a card', () => {
  const seen: unknown[] = []
  const fallback = (token: Tokens.Code) => {
    seen.push(token)
    return 'PLAIN-CODE-BLOCK'
  }
  // Both legs use the identical fixture; only the plane flag differs, so the
  // embedded (negative) leg is anchored to a positive one it can be compared
  // against. The rule exists because POST /api/v1/agent/actions/execute sits
  // behind the member-JWT gate: an embed visitor must not be handed a card whose
  // every button answers 401.
  const embedded = createSurfaceAwareCodeRenderer(true, fallback)
  assert.equal(embedded(codeToken(CJK_FRAMES, 'a2ui')), 'PLAIN-CODE-BLOCK')
  assert.deepEqual(seen, [codeToken(CJK_FRAMES, 'a2ui')])

  const normal = createSurfaceAwareCodeRenderer(false, fallback)
  assert.match(normal(codeToken(CJK_FRAMES, 'a2ui')), /^<div class="chat-a2ui-surface"/)
  assert.equal(seen.length, 1, 'the non-embedded leg must not reach the fallback')
})

// The official tag literals are spelled out below, never imported from
// A2UI_TAG_OPEN/A2UI_TAG_CLOSE: the rewriter, the placeholder builder and the
// expectation reading one symbol would stay green while all of them said
// something the official parser ignores (that is exactly how the ```a2ui fence
// survived — constant, teaching and frontend all read the same literal).
const TAG_OPEN = '<a2ui-json>'
const TAG_CLOSE = '</a2ui-json>'

/** All placeholders in one rendered string, in document order. */
function placeholders(html: string): string[] {
  return html.match(/<div class="chat-a2ui-surface"[^>]*><\/div>/g) ?? []
}

test('markA2uiTagBlocks turns an official tag block into the fence placeholder', () => {
  const md = `前置说明\n${TAG_OPEN}\n${CJK_FRAMES}\n${TAG_CLOSE}\n后置说明`
  const out = markA2uiTagBlocks(md)

  // Same placeholder the fence path emits, so the mounter below is untouched.
  assert.deepEqual(placeholders(out), [a2uiPlaceholderHtml(CJK_FRAMES)])
  assert.ok(out.startsWith('前置说明\n'), `prose before the card was eaten: ${out.slice(0, 40)}`)
  assert.ok(out.endsWith('\n后置说明'), 'prose after the card was eaten')
  assert.ok(!out.includes('a2ui-json'), 'the tags must not survive into the html')

  const attr = placeholders(out)[0].match(/data-a2ui-frames="([^"]*)"/)
  if (!attr) throw new Error('payload attribute missing')
  const frames = decodeA2uiFrames(attr[1]) as A2uiFrame[]
  assert.equal(frames.length, 2)
  assert.equal(frames[1].updateComponents?.components.find((c) => c.id === 'lbl')?.text, '知道了')
})

test('the tag rewriter has exactly the official tolerances, and no more', () => {
  // blocks.py:26 — case-insensitive, attributes allowed on the open tag.
  assert.ok(
    markA2uiTagBlocks(`<A2UI-JSON data-source="weknora">${CJK_FRAMES}</A2UI-JSON>`)
      .includes(a2uiPlaceholderHtml(CJK_FRAMES)),
    'uppercase tag / attributed open tag must be accepted (official OPEN_TAG_RE)',
  )
  // blocks.py:31-32 — a fence wrapped around the payload inside the tags is noise.
  assert.ok(
    markA2uiTagBlocks(`${TAG_OPEN}\n\`\`\`json\n${CJK_FRAMES}\n\`\`\`\n${TAG_CLOSE}`)
      .includes(a2uiPlaceholderHtml(CJK_FRAMES)),
    'a fenced payload inside the tags must be un-fenced like the official parser does',
  )
  // Two sibling blocks are two cards, in document order.
  const two = `${TAG_OPEN}[{"version":"v0.9","createSurface":{"surfaceId":"a"}}]${TAG_CLOSE}`
    + `\nmid\n${TAG_OPEN}[{"version":"v0.9","createSurface":{"surfaceId":"b"}}]${TAG_CLOSE}`
  const ids = placeholders(markA2uiTagBlocks(two)).map(
    (p) => (decodeA2uiFrames(p.match(/data-a2ui-frames="([^"]*)"/)![1]) as A2uiFrame[])[0].createSurface?.surfaceId,
  )
  assert.deepEqual(ids, ['a', 'b'])
})

test('a tag pair quoted inside the card payload stays one card', () => {
  // blocks.py BlockScanner: "A tag written inside a JSON string does not count."
  // A card that documents the format — WeKnora answering "怎么发一张 A2UI 卡片" —
  // carries a full pair inside a Text value. A plain regex ends the block at the
  // quoted close tag, truncating the payload.
  const payload = JSON.stringify([{
    version: 'v0.9',
    updateComponents: {
      surfaceId: 's1',
      components: [{ id: 'root', component: 'Text', text: `写卡片要写 ${TAG_OPEN}[]${TAG_CLOSE}` }],
    },
  }])
  const out = markA2uiTagBlocks(`${TAG_OPEN}\n${payload}\n${TAG_CLOSE}`)
  const found = placeholders(out)
  assert.equal(found.length, 1, 'the quoted close tag must not split the card in two')
  const frames = decodeA2uiFrames(found[0].match(/data-a2ui-frames="([^"]*)"/)![1]) as A2uiFrame[]
  assert.equal(frames[0].updateComponents?.surfaceId, 's1')
  assert.ok(frames[0].updateComponents?.components[0].text?.includes(TAG_OPEN))
})

test('no card, no placeholder: the rewriter leaves the source visible', () => {
  // Anchors for every positive leg above: the wrapper is what makes a card, so
  // each negative leg is the same payload with the wrapper missing, and each has
  // a positive leg next to it that says the payload itself is fine.
  assert.equal(markA2uiTagBlocks(CJK_FRAMES), CJK_FRAMES, 'a bare array has no wrapper to find')
  assert.equal(markA2uiTagBlocks('这段回答里没有卡片。'), '这段回答里没有卡片。')
  assert.equal(markA2uiTagBlocks(`${CJK_FRAMES}\n${TAG_CLOSE}`), `${CJK_FRAMES}\n${TAG_CLOSE}`,
    'a close tag with no opener is not a card')

  // Unclosed (still streaming, or a hallucination): fail closed by leaving the
  // source on screen — the documented fallback is "the source stays visible".
  const unclosed = `前文\n${TAG_OPEN}\n[{"version":"v0.9","createSurface":`
  const out = markA2uiTagBlocks(unclosed)
  assert.deepEqual(placeholders(out), [])
  assert.ok(out.includes(TAG_OPEN), 'an unclosed block must stay visible as source text')

  // An empty payload would mount an empty shell; the backend drops it too.
  assert.deepEqual(placeholders(markA2uiTagBlocks(`${TAG_OPEN}\n\n${TAG_CLOSE}`)), [])
})

test('cleanA2uiPayload is blocks.py clean(): fence off, backtick-only is empty', () => {
  assert.equal(cleanA2uiPayload(`\n\`\`\`json\n${CJK_FRAMES}\n\`\`\`\n`), CJK_FRAMES)
  assert.equal(cleanA2uiPayload(CJK_FRAMES), CJK_FRAMES)
  assert.equal(cleanA2uiPayload('   \n\`\`\`\n\n'), '')
})

const SKELETON = '<div class="chat-a2ui-pending-card" role="status">'
  + '<span class="chat-a2ui-pending-card__spinner"></span>'
  + '<span class="chat-a2ui-pending-card__label">正在生成卡片…</span></div>'

test('unclosed a2ui-json tag during streaming shows skeleton, hides JSON', () => {
  const partial = `前置\n${TAG_OPEN}\n[{"version":"v0.9","createSurface":{"surfaceId":`
  // streaming=true → skeleton
  const out = markA2uiTagBlocks(partial, true)
  assert.ok(out.includes(SKELETON), 'skeleton must appear during streaming')
  assert.ok(!out.includes('a2ui-json'), 'no raw tag text may leak')
  assert.ok(!out.includes('createSurface'), 'no JSON payload visible')

  // streaming=false (default) → source stays visible (fail-closed unchanged)
  const out2 = markA2uiTagBlocks(partial)
  assert.ok(out2.includes(TAG_OPEN), 'fail-closed: source stays visible when not streaming')
  assert.ok(!out2.includes(SKELETON), 'no skeleton when not streaming')
})

test('closed a2ui-json block during streaming still gets the normal placeholder, not the skeleton', () => {
  const md = `说明${TAG_OPEN}\n${CJK_FRAMES}\n${TAG_CLOSE}结尾`
  const out = markA2uiTagBlocks(md, true)
  assert.ok(out.includes(a2uiPlaceholderHtml(CJK_FRAMES)), 'closed block → base64 placeholder')
  assert.ok(!out.includes(SKELETON), 'skeleton must not swallow a closed block')
})

test('a2uiPendingCardHtml returns the exact skeleton markup', () => {
  assert.equal(a2uiPendingCardHtml(), SKELETON)
  // Contains no data- attributes at all (DOMPurify needs no extra config)
  assert.ok(!a2uiPendingCardHtml().includes('data-'))
})
