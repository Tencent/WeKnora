import type { App } from 'vue'
import type { Renderer } from 'marked'
// Relative imports + explicit .ts extensions, not written casually: these two modules get loaded inside node
// by a2uiSurface.test.ts together with its host module, and tsx --test doesn't understand vite's '@' alias (it throws
// ERR_MODULE_NOT_FOUND and the entire test file goes red outright). Sibling modules under src/utils always cross-reference via relative paths (38 prior instances).
import { weknoraCatalog } from './a2uiCatalog.ts'
import { weknoraTheme } from './a2uiTheme.ts'

/**
 * Official A2UI v0.9 basic-catalog identifier, emitted by the WeKnora backend
 * in every createSurface frame (see internal/agent/prompts.go and the
 * internal/types fixtures). a2ui-vue's own default catalogId is a different
 * URL (…/basic_catalog.json), and the core processor matches createSurface
 * frames to catalogs by EXACT id (it throws "Catalog not found" otherwise),
 * so we must pin this value when we call provideA2UI for a mounted surface.
 */
export const A2UI_BASIC_CATALOG_ID =
  'https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json'

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder()

/** UTF-8-safe base64 of an arbitrary JSON string (CJK labels survive). */
export function encodeA2uiFrames(json: string): string {
  const bytes = textEncoder.encode(json)
  let bin = ''
  for (let i = 0; i < bytes.length; i += 1) {
    bin += String.fromCharCode(bytes[i])
  }
  return btoa(bin)
}

/** Base64 → text. Throws only on malformed base64, NOT on bad JSON. */
export function decodeA2uiPayloadText(b64: string): string {
  const bin = atob(b64)
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
  return textDecoder.decode(bytes)
}

/**
 * Parse a fence body into the frame list a MessageProcessor expects.
 *
 * Two shapes are accepted because that is exactly what the backend accepts:
 * `ExtractA2UIMessages` (internal/types/agent_card.go) tolerates a model that
 * emits a single message OBJECT instead of an array. Refusing the object here
 * while the server executes its buttons would leave the user staring at a card
 * that exists only server-side, so the two sides must agree.
 */
export function parseA2uiFrames(text: string): unknown[] {
  const parsed: unknown = JSON.parse(text)
  if (Array.isArray(parsed)) return parsed
  if (parsed !== null && typeof parsed === 'object') return [parsed]
  throw new Error('a2ui frames payload is not a JSON object or array')
}

/** Decode + parse; throws on malformed base64, invalid JSON or a scalar payload. */
export function decodeA2uiFrames(b64: string): unknown[] {
  return parseA2uiFrames(decodeA2uiPayloadText(b64))
}

/**
 * Build the placeholder DOMPurify passes through: a bare div carrying the
 * frame payload base64-encoded in a data-* attribute (the attribute alphabet
 * is [A-Za-z0-9+/=], so no HTML escaping is needed) plus a state flag the
 * mounter uses to avoid double-mounting.
 */
export function a2uiPlaceholderHtml(framesJson: string): string {
  const payload = encodeA2uiFrames(framesJson)
  return `<div class="chat-a2ui-surface" data-a2ui-frames="${payload}" data-a2ui-state="pending"></div>`
}

/**
 * Streaming-only placeholder: shown while an `<a2ui-json>` open tag has
 * arrived but the close tag has not, so the user sees a card skeleton
 * instead of the raw JSON payload. The skeleton is plain div/span markup —
 * no data attributes, no inline styles — so DOMPurify passes it through
 * with the existing markdown config (no FORBID_ATTR / USE_PROFILES change).
 */
export function a2uiPendingCardHtml(): string {
  return (
    '<div class="chat-a2ui-pending-card" role="status">'
    + '<span class="chat-a2ui-pending-card__spinner"></span>'
    + '<span class="chat-a2ui-pending-card__label">正在生成卡片…</span></div>'
  )
}

/**
 * Official A2UI direct_json wrapper tags — the literals the official SDK writes
 * into the prompt it generates and parses with (a2ui/constants.py
 * A2UI_OPEN_TAG/A2UI_CLOSE_TAG, blocks.py:24-27). This is the form the WeKnora
 * agent prompt has always emitted; the ```a2ui fence below is an earlier
 * self-contained approach and stays accepted for messages already stored.
 */
export const A2UI_TAG_OPEN = '<a2ui-json>'
export const A2UI_TAG_CLOSE = '</a2ui-json>'

// Copied from the official parser, so the tolerance is theirs and not ours:
// blocks.py:26 OPEN_TAG_RE, :27 CLOSE_TAG_RE, :31-32 _LEADING/TRAILING_FENCE_RE.
const a2uiOpenTagRE = /<a2ui-json(?:\s[^>]*)?>/i
const a2uiCloseTagRE = /<\/a2ui-json\s*>/i
const a2uiLeadingFenceRE = /^```[a-zA-Z-]*\s*/
const a2uiTrailingFenceRE = /\s*```[a-zA-Z-]*$/

/**
 * blocks.py clean(): trim, strip one leading and one trailing markdown fence,
 * trim again; a payload that is nothing but fence backticks is empty. A model
 * that fences the tags anyway is absorbed here exactly like the official parser
 * absorbs it, so we do not lose a card the official renderer would have shown.
 */
export function cleanA2uiPayload(body: string): string {
  let text = body.trim()
  text = text.replace(a2uiLeadingFenceRE, '')
  text = text.replace(a2uiTrailingFenceRE, '')
  text = text.trim()
  return text.replace(/`/g, '') === '' ? '' : text
}

/**
 * Find the first `</a2ui-json>` at or after `start` that is not inside a JSON
 * string — the rule blocks.py BlockScanner states verbatim ("A tag written
 * inside a JSON string does not count"). Returns the close tag's offset and
 * length, or -1 when the block never closes (streaming tail, or a hallucination:
 * fail closed, show the source).
 */
function findA2uiCloseTag(text: string, start: number): { index: number; length: number } {
  for (let i = start, inString = false; i < text.length;) {
    if (inString) {
      const quote = text.indexOf('"', i)
      const end = quote < 0 ? text.length : quote
      // String.indexOf has no end argument (unlike the Python str.find this
      // mirrors), so the escape search is bounded by slicing to the next quote.
      const escape = text.slice(i, end).indexOf('\\')
      if (escape >= 0) {
        i += escape + 2 // escaped character: skip both bytes
        continue
      }
      if (quote < 0) break // string never closes: no reliable close tag
      inString = false
      i = quote + 1
      continue
    }
    if (text[i] === '"') {
      inString = true
      i += 1
      continue
    }
    if (text[i] === '<') {
      const m = a2uiCloseTagRE.exec(text.slice(i))
      if (m && m.index === 0) return { index: i, length: m[0].length }
    }
    i += 1
  }
  return { index: -1, length: 0 }
}

/**
 * Replace every complete `<a2ui-json>` block in the answer's SOURCE markdown
 * with the same surface placeholder the ```a2ui fence path produces, so the card
 * mounts. This runs before marked parses the answer, which is the whole point:
 *
 * - the payload is base64'd into a data attribute here, so no JSON brace, CJK
 *   label or `<` can reach marked's inline parser, the math-delimiter rewrite,
 *   the emphasis balancers or DOMPurify as markup/text to interpret;
 * - a card we cannot mark is a card the user must still see: an unclosed tag
 *   with `streaming = false` (the stream ended and it never closed —
 *   hallucination or truncation) or an empty payload leaves the source
 *   untouched, and DOMPurify dropping the unknown tag leaves its payload
 *   visible as plain text — the documented fallback is "the source stays
 *   visible";
 * - while streaming (`streaming = true`) an unclosed block is the NORMAL state
 *   (the close tag is still in flight), not a failure: the partial payload is
 *   replaced by a card-skeleton placeholder (a2uiPendingCardHtml) so the user
 *   sees "a card is being generated" instead of raw JSON. The next frame after
 *   the close tag arrives takes the closed-block path above.
 *
 * No markdown-fence awareness, deliberately: the official parser scans raw text
 * and treats fences as noise, and the backend extractor (internal/types
 * agent_card.go scanA2UITagBlocks) does the same, so a code sample that quotes a
 * full tag pair becomes a card on both sides. Agreement between the producer,
 * the server and this renderer is the contract; a frontend that was cleverer
 * than the backend would show a card the server refuses to execute.
 */
export function markA2uiTagBlocks(markdown: string, streaming = false): string {
  // Cheap pre-filter, deliberately looser than the open-tag regex: it drops the
  // trailing ">", because the official open tag may carry attributes, and it is
  // case-folded, because the tag is matched case-insensitively. Requiring
  // '<a2ui-json>' here would drop `<A2UI-JSON data-source="weknora">…`, a card
  // the official parser accepts — the same trap the backend pre-filter fell into.
  if (!markdown || !markdown.toLowerCase().includes(A2UI_TAG_OPEN.slice(0, -1))) return markdown
  let out = ''
  let i = 0
  while (i < markdown.length) {
    const rest = markdown.slice(i)
    const open = a2uiOpenTagRE.exec(rest)
    if (!open || open.index < 0) {
      out += rest
      break
    }
    const blockStart = i + open.index
    const payloadStart = blockStart + open[0].length
    const close = findA2uiCloseTag(markdown, payloadStart)
    if (close.index < 0) {
      if (streaming) {
        // Unclosed during streaming: skeleton placeholder, partial JSON stays invisible (the next frame after the close tag arrives swaps in the normal placeholder).
        out += markdown.slice(i, blockStart) + a2uiPendingCardHtml()
      } else {
        // Fail closed: the stream has ended and it still never closed (hallucination / truncation) → the source stays visible.
        out += markdown.slice(i)
      }
      break
    }
    const payload = cleanA2uiPayload(markdown.slice(payloadStart, close.index))
    out += markdown.slice(i, blockStart)
    // An empty payload would mount a shell with nothing in it; the backend
    // drops such a block too, so keep the source on screen.
    out += payload ? a2uiPlaceholderHtml(payload) : markdown.slice(blockStart, close.index + close.length)
    i = close.index + close.length
  }
  return out
}

// marked's own code slot, not a hand-narrowed token shape. That distinction is
// what makes the wrapper composable in both directions: the mermaid renderer is
// typed against the full `Tokens.Code` (it needs `type`/`raw`), so a slot typed
// as a narrow `{ text, lang }` callback would REJECT it — parameters are
// contravariant. Typing the slot as `Renderer['code']` accepts both that
// renderer and, in tests, a handler that only reads `{ text, lang }`, because a
// function with a wider parameter is assignable to a narrower-param slot.
// Type-only import: erased at runtime, so `tsx --test` still runs this file.
type ChatCodeRenderer = Renderer['code']

/**
 * Wrap an existing code renderer so that ```a2ui fences become surface
 * placeholders instead of code blocks. Every other fence (mermaid included)
 * falls through to the inner renderer unchanged.
 */
export function createA2uiCodeRenderer(fallback: ChatCodeRenderer): ChatCodeRenderer {
  return (token) => {
    if (token.lang === 'a2ui') {
      return a2uiPlaceholderHtml(token.text)
    }
    return fallback(token)
  }
}

/**
 * The plane-aware variant both chat hosts install. On the embedded plane the
 * fence must stay a plain code block: the action executor sits behind the
 * member-JWT gate (`g.Viewer()` in routes_chat.go), so an embed visitor would
 * get a rendered card whose every button answers 401. Emitting no placeholder
 * at all is what keeps that plane honest — the fence is shown, not faked.
 */
export function createSurfaceAwareCodeRenderer(
  embedded: boolean,
  fallback: ChatCodeRenderer,
): ChatCodeRenderer {
  return embedded ? fallback : createA2uiCodeRenderer(fallback)
}

/**
 * Nested surface apps, keyed by the message that owns them.
 *
 * Vue only tears a component tree down through `app.unmount()`. When the answer
 * container is rewritten (v-html refresh on a follow-up turn, theme switch, a
 * rewind re-render), the placeholder element and everything mounted inside it
 * are dropped at the DOM level: no unmount hook fires, so the old app, its
 * reactive effects and its `onEvent` subscription would all leak. Recording the
 * app under its message lets the next mount pass for that message reclaim the
 * slot, and lets hosts release their cards from `onBeforeUnmount`.
 *
 * The key is only as good as the caller's discipline: release must pass the
 * very id used at mount time. Both hosts therefore freeze the id on their first
 * mount rather than recomputing it, because the persisted assistant id can
 * arrive after the answer finished rendering — a teardown that derives a
 * different id then looks up a slot that was never registered, and the entry
 * stays in this module-level map forever with its app and its `onEvent`
 * subscription attached. There is deliberately no "release everything" helper:
 * one message's host unmounting must not tear down its siblings' cards, so an
 * id-less teardown would be wrong, not merely blunt.
 */
const liveSurfaceApps = new Map<string, App[]>()

// Coerced so a host that passes an undefined optional sessionId on the mount
// path cannot register a slot the teardown path then fails to look up.
const surfaceKey = (sessionId: string, messageId: string): string =>
  `${sessionId ?? ''} ${messageId ?? ''}`

/** Release every surface app belonging to one message. Idempotent. */
export function unmountA2uiSurfaces(sessionId: string, messageId: string): void {
  const key = surfaceKey(sessionId, messageId)
  const apps = liveSurfaceApps.get(key)
  if (!apps) return
  liveSurfaceApps.delete(key)
  for (const app of apps) app.unmount()
}

/**
 * Show the fence body as a code block when we cannot render it. The placeholder
 * already replaced the `<pre>` marked produced, so without this the message
 * would just show an unexplained blank gap — the backend contract
 * ("fallback: a plain code block") is that the source stays visible. Built with
 * textContent, so the model's text is never parsed as HTML.
 */
function renderA2uiFallback(el: HTMLElement, raw: string): void {
  el.dataset.a2uiState = 'failed'
  el.className = 'chat-a2ui-fallback'
  const pre = document.createElement('pre')
  const code = document.createElement('code')
  code.textContent = raw.trim() || '(empty a2ui fence)'
  pre.appendChild(code)
  el.appendChild(pre)
}

/**
 * Scan a rendered message container for pending a2ui placeholders and mount a
 * live A2UI surface into each. Called from botmsg / AgentStreamDisplay once the
 * typewriter has fully revealed the answer, so the fence body is complete.
 * The a2ui-vue module is imported lazily so it only loads when a card exists.
 */
export async function mountA2uiSurfaces(
  root: HTMLElement | null | undefined,
  sessionId: string,
  messageId: string,
): Promise<void> {
  if (!root) return
  // Claim the placeholders synchronously, in the same tick as the query and
  // before the first await. Hosts call this from both onMounted and onUpdated,
  // and those overlap while the lazily-imported a2ui-vue chunk is still in
  // flight; without the claim both passes would see the same `pending` elements
  // and mount two apps into one div, leaking the first one forever.
  const claimed = Array.from(
    root.querySelectorAll<HTMLElement>('.chat-a2ui-surface[data-a2ui-state="pending"]'),
  )
  if (claimed.length === 0) return
  for (const el of claimed) el.dataset.a2uiState = 'mounting'

  // Anything still recorded for this message belonged to a DOM subtree that has
  // since been replaced (see liveSurfaceApps).
  unmountA2uiSurfaces(sessionId, messageId)

  const { createApp } = await import('vue')
  const { provideA2UI, DEFAULT_CATALOG, defaultTheme } = await import('a2ui-vue')
  const { default: A2uiCard } = await import(
    '@/views/chat/components/a2ui/A2uiCard.vue'
  )

  // Computed once up front instead of rebuilt inside the per-element loop (catalog/theme are both immutable config).
  // Our own overrides: the Button component and additionalStyles — see a2uiCatalog.ts / a2uiTheme.ts.
  const catalog = weknoraCatalog(DEFAULT_CATALOG)
  const theme = weknoraTheme(defaultTheme)

  const mounted: App[] = []
  for (const el of claimed) {
    const b64 = el.dataset.a2uiFrames
    let raw = ''
    let frames: unknown[]
    try {
      // Decode the text first, then parse: a payload that is valid base64 but
      // not valid JSON still yields its source text, which is what the fallback
      // needs to show instead of swallowing the fence.
      raw = decodeA2uiPayloadText(b64 ?? '')
      frames = parseA2uiFrames(raw)
    } catch (err) {
      // The model emitted a broken card. Show its source and mark it settled so
      // we do not retry on every update.
      console.error('[a2ui] could not decode surface frames', err)
      renderA2uiFallback(el, raw)
      continue
    }
    try {
      const app = createApp(A2uiCard, { frames, sessionId, messageId })
      provideA2UI({
        app,
        catalog,
        theme,
        catalogId: A2UI_BASIC_CATALOG_ID,
      })
      app.mount(el)
      el.dataset.a2uiState = 'mounted'
      mounted.push(app)
    } catch (err) {
      console.error('[a2ui] failed to mount surface', err)
      renderA2uiFallback(el, raw)
    }
  }
  if (mounted.length > 0) {
    liveSurfaceApps.set(surfaceKey(sessionId, messageId), mounted)
  }
}
