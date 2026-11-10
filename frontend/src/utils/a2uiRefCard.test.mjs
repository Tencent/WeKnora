import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync, existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// Acceptance assertions for the definition-confirmation reference card style. Reference-card
// facts: white card, 14px radius + an extremely light shadow;
// body text 13.5~14px / line-height 1.72; the primary button is solid green (we keep
// --td-brand-color) with 8px radius, 33px height, 13px type, 500 weight and a green shadow;
// the default button is white with a border. The spec table replicates the reference card's
// .specs/.spec verbatim: the model wraps spec rows in a nested Column (confirmed in the live
// DOM), and that nested Column is the table container (secondary background + border +
// 10px radius + overflow:hidden), with rows keeping only a bottom 1px divider. The reference
// card's .card-head (icon + title bar) has no corresponding component primitive, so the
// header is not replicated.

const FRONTEND_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')

// Values used verbatim by the reference card — assertion values must be these literals and share no symbols with the implementation files.
const CARD_RADIUS = '14px'
const CARD_SHADOW_1 = '0 1px 2px rgba(16, 24, 40, 0.04)'
const CARD_SHADOW_2 = '0 1px 3px rgba(16, 24, 40, 0.03)'
const BTN_HEIGHT = '33px'
const BTN_PADDING = '0 15px'
const BTN_RADIUS = '8px'
const BTN_FONT_SIZE = '13px'
const BTN_FONT_WEIGHT = '500'
const BTN_GAP = '6px'
const BTN_PRIMARY_SHADOW = '0 2px 8px'
const DEFAULT_BTN_BG = 'var(--td-bg-color-container)'

const cssPath = join(FRONTEND_ROOT, 'src', 'assets', 'theme', 'a2ui-card.css')
const vuePath = join(FRONTEND_ROOT, 'src', 'views', 'chat', 'components', 'a2ui', 'A2uiButton.vue')

// Negative anchor: these strings must **not** appear in the build output — guards against cutting corners with the library's purple gradient / the reference card's blue primary / raw green rgba.
const NEGATIVE_PATTERNS = [
  ['#1E5BFF', '参照卡的蓝色主色被误抄进我方样式'],
  ['#EDF3FF', '参照卡蓝色浅底被误抄'],
  ['linear-gradient', 'a2ui-vue 的紫色渐变底不应出现'],
  ['rgba(7, 192, 95', '绿色投影必须走 color-mix(var(--td-brand-color))，裸 rgba 会被 styleGuard 的 brand-rgba(基线0) 抓住且深色模式不跟随'],
]

function read(p) {
  assert.ok(existsSync(p), `缺少待测文件：${p} —— 参照卡改造必须先写代码再谈验收`)
  return readFileSync(p, 'utf8')
}

const css = read(cssPath)
const vue = read(vuePath)

test('卡片容器：14px 圆角走 --app-radius-xxl，不再用 lg(10px)', () => {
  assert.match(css, /border-radius:\s*var\(--app-radius-xxl\)/, 'a2ui-card.css 里找不到 var(--app-radius-xxl)')
  assert.doesNotMatch(css, /a2ui-card[^{]*\{[^}]*border-radius:\s*var\(--app-radius-lg\)/, '卡片圆角仍停留在 10px（--app-radius-lg）')
  // The token itself must really be defined as 14px in theme.css, not be yet another dangling reference
  const theme = read(join(FRONTEND_ROOT, 'src', 'assets', 'theme', 'theme.css'))
  assert.match(theme, /--app-radius-xxl:\s*14px/, 'theme.css 里没有 --app-radius-xxl: 14px，CSS 里的引用是空引用')
})

test('卡片容器：参照卡的极轻双层投影', () => {
  assert.match(css, /0\s*1px\s*2px\s*rgba\(16,\s*24,\s*40,\s*0\.04\)/, '缺 0 1px 2px rgba(16, 24, 40, 0.04)')
  assert.match(css, /0\s*1px\s*3px\s*rgba\(16,\s*24,\s*40,\s*0\.03\)/, '缺 0 1px 3px rgba(16, 24, 40, 0.03)')
})

test('按钮：33px 高 / 8px 圆角 / 13px 字 / 500 字重 / 6px 间距', () => {
  assert.match(vue, new RegExp(`height:\\s*${BTN_HEIGHT}`), `按钮高度不是 ${BTN_HEIGHT}（参照卡 .btn 的高度）`)
  assert.match(vue, /border-radius:\s*var\(--app-radius-md\)/, '按钮圆角必须走 --app-radius-md(8px)，不写裸 8px')
  assert.match(vue, /font-size:\s*var\(--app-text-md\)/, '按钮字号必须走 --app-text-md(13px)，不写裸 13px')
  assert.match(vue, /font-weight:\s*500/, `按钮字重应为 ${BTN_FONT_WEIGHT}`)
  assert.match(vue, new RegExp(`gap:\\s*${BTN_GAP}`), `按钮图标与文字的间距应为 ${BTN_GAP}`)
})

test('按钮 padding：0 15px（参照卡 .btn）', () => {
  assert.match(vue, new RegExp(`padding:\\s*${BTN_PADDING}`), `按钮左右内距不是 ${BTN_PADDING}`)
})

test('主按钮：实心品牌绿 + 绿色投影（color-mix 推导，非裸 rgba）', () => {
  const m = vue.match(/\.a2ui-btn--primary\s*\{[^}]*box-shadow:[^;]*;?[^}]*\}/)
  assert.ok(m, '主按钮没有 box-shadow —— 参照卡 .btn-primary 的立体感丢了')
  assert.match(m[0], /color-mix\(in srgb,\s*var\(--td-brand-color\)\s+26%,\s*transparent\)/, '绿色投影必须用 color-mix 从品牌色推导')
  assert.match(m[0], new RegExp(BTN_PRIMARY_SHADOW), `投影尺寸应为 ${BTN_PRIMARY_SHADOW}`)
})

test('主按钮文字钉死白色（深浅模式都白：字面量 #fff，不许回到会翻转的 --td-text-color-anti）', () => {
  // Capture the whole .a2ui-btn--primary rule body (no closing brace inside the block)
  const primary = vue.match(/\.a2ui-btn--primary\s*\{[^}]*\}/)
  assert.ok(primary, '缺 .a2ui-btn--primary 规则')
  assert.match(primary[0], /color:\s*#fff/, '主按钮文字必须是字面量 #fff —— 用户要求深浅模式都用白')
  // Negative anchor: --td-text-color-anti is an "inverted color" token that flips to a dark color in dark mode — exactly the root cause of the green background going dim
  assert.doesNotMatch(primary[0], /--td-text-color-anti/, '主按钮文字不许回到会随主题翻转的 --td-text-color-anti')
  // The label is rendered by a child component; :deep pins the white inside the button, so the library's a2ui-text color scale cannot override the inherited color
  assert.match(vue, /\.a2ui-btn--primary\s+:deep\(\*\)\s*\{\s*color:\s*#fff;?\s*\}/, '主按钮必须用 :deep(*) 把 #fff 钉到内部标签上')
})

test('默认按钮：白底（参照卡 .btn 的白底描边）', () => {
  const m = vue.match(/\.a2ui-btn--default\s*\{[^}]*\}/)
  assert.ok(m, '缺 .a2ui-btn--default 规则')
  assert.match(m[0], /background-color:\s*var\(--td-bg-color-container\)/, `默认按钮底应为 ${DEFAULT_BTN_BG}（白底），现在是浅灰 secondarycontainer`)
})

test('负锚：参照卡的蓝、库的紫渐变、裸绿 rgba 一个都不许出现', () => {
  for (const [pat, why] of NEGATIVE_PATTERNS) {
    assert.doesNotMatch(css, new RegExp(pat.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `a2ui-card.css 出现 ${pat} —— ${why}`)
    assert.doesNotMatch(vue, new RegExp(pat.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `A2uiButton.vue 出现 ${pat} —— ${why}`)
  }
})

test('正文：14px / 行高 1.72，只对非标题的 Text 宿主生效', () => {
  assert.match(css, /:not\(:has\(\s*>\s*:is\(h1,\s*h2,\s*h3,\s*h4,\s*h5,\s*h6\)\)\)/, '正文规则必须用 :not(:has(标题)) 限定，否则会压掉标题的 em 基准')
  assert.match(css, /font-size:\s*var\(--app-text-base\)/, '正文字号应引用 --app-text-base(14px)')
  assert.match(css, /line-height:\s*1\.72/, '正文行高应为参照卡的 1.72')
})

// Spec table (reference card .specs/.spec: one rounded table background for the whole block +
// 1px row dividers + same-column alignment). Structural fact: the model wraps spec rows in a
// nested a2ui-column, and that nested Column's section is the table container.
// Reference-card facts: .specs{background:#FAFBFD;border:1px solid;border-radius:10px;overflow:hidden};
// .spec{padding:9px 12px;border-bottom:1px solid}; .spec .k{width:74px;flex:none;font-size:12.5px}.
// 2026-11-06 revision 2: the tracks only govern two-column layouts. A wide table (any row with
// ≥3 cells; live session 1541affe, "demo table: 4 columns × 5 rows") had its rows torn apart by
// the 2-column tracks, so it now takes the classic table-layout branch — the four display:table*
// assertions at the end of this section.
// 2026-11-06 revision 1: the .k's 74px is no longer replicated — live measurement (session
// 5bd01b9b, card "render the A2UI confirmation card") found long CJK label/value pairs like a
// 9-character first-column label and an "800,000+" value squeezed onto two lines by 74px (the
// first-column box measured exactly 74px wide). Column widths are now computed by the container
// grid tracks (auto + minmax(50%, 1fr)); flattening row boxes into the grid is carried by the two
// display:contents rules.
// Our background/border go through --td-* tokens (dark mode must flip them); the radius uses --app-radius-lg(10px).
test('规格表：容器是嵌套 Column 的 section —— 底色 + 边框 + 10px 圆角 + overflow:hidden + 与上文 12px 间距', () => {
  // Expected values written as literals (no shared symbols with the implementation). Three parallel functional
  // pseudo-classes, each with at most one :has inside — :has(> a2ui-row) locks onto a column holding rows,
  // :not(:has(> a2ui-text)) excludes the outer Column (its direct children include the two bare Texts
  // heading/goal), and :not(:has(> a2ui-row > .a2ui-btn-host)) excludes a column holding only the button row.
  // Never write back the nested form :has(> a2ui-row > section:has(...)): Chrome 134 silently drops the whole
  // rule, the previous version was written exactly that way, and the result was "spec table with no background
  // and no border" while this test still ran green.
  assert.match(css, /\.chat-a2ui-surface\s+a2ui-column\s*>\s*section:has\(>\s*a2ui-row\):not\(:has\(>\s*a2ui-text\)\):not\(:has\(>\s*a2ui-row\s*>\s*\.a2ui-btn-host\)\)\s*\{/, '容器选择器必须是"一个 :has + 两个 :not(:has)"的非嵌套形式，少一个 :not 会把整张卡染成表格底，改成嵌套则整条被浏览器丢弃')
  assert.match(css, /background-color:\s*var\(--td-bg-color-secondarycontainer\)/, '表格底应走 --td-bg-color-secondarycontainer（随主题翻转），不能写死参照卡的 #FAFBFD')
  assert.match(css, /border:\s*1px solid var\(--td-component-border\)/, '表格容器描边应走 --td-component-border')
  assert.match(css, /border-radius:\s*var\(--app-radius-lg\)/, '表格圆角必须走 --app-radius-lg(10px，参照卡 .specs 的 10px)，不写裸 10px')
  assert.match(css, /overflow:\s*hidden/, '容器必须 overflow:hidden 裁圆角，否则行底色会戳出圆角')
  assert.match(css, /margin-top:\s*12px/, '表格与目标行之间必须留参照卡 .card-goal 的 12px 间距')
  // The old "every row carries its own four-sided border + -1px overlapping line" form must stay gone — that was the rejected stacked-box look
  assert.doesNotMatch(css, /margin-top:\s*-1px/, '行间分隔线必须改为 border-bottom，不能再靠 margin-top:-1px 叠线')
})

test('规格表：行内只留底部 1px 分隔线，最后一行不留（不与容器底框叠成双线）', () => {
  assert.match(css, /a2ui-row\s*>\s*section:has\(>\s*a2ui-text\)\s*\{[^}]*padding:\s*9px 12px[^}]*gap:\s*12px[^}]*border-bottom:\s*1px solid var\(--td-component-border\)/, '规格行必须 padding:9px 12px + gap:12px + border-bottom:1px solid（参照卡 .spec），且不得再给整行四边边框')
  assert.match(css, /a2ui-row:last-child\s+a2ui-text\s*\{\s*border-bottom:\s*none/, '表格末行格子必须 border-bottom:none，否则与容器底框叠成 2px（行盒已被 contents 拍平，分隔线挂在格子上）')
})

// Root-cause guardrail caught on a live browser this round: a nested :has in a stylesheet is not a
// "degradation" — the whole rule evaporates. Unit tests only read source text and cannot see this class
// of failure, so the browser's constraint is pinned here as a source-side invariant.
test('护栏：本文件任何选择器都不许把 :has() 套进 :has()', () => {
  // Decision implementation: for each :has( find its matching closing paren, then check whether that span contains a second :has(
  const hasNestedHas = (sel) => {
    let from = 0
    for (;;) {
      const a = sel.indexOf(':has(', from)
      if (a < 0) return false
      let depth = 1
      let j = a + ':has('.length
      while (j < sel.length && depth > 0) {
        if (sel[j] === '(') depth++
        else if (sel[j] === ')') depth--
        j++
      }
      if (sel.slice(a + ':has('.length, j - 1).includes(':has(')) return true
      from = a + ':has('.length
    }
  }
  // Self-test anchor: the decision must first recognize a real nested case (exactly the previous-version form
  // that was killed and left the spec table without a background for a whole round); otherwise the "zero hits"
  // assertion below spins idle — with a broken criterion, an artifact that produces nothing would still pass green.
  assert.equal(
    hasNestedHas('.chat-a2ui-surface a2ui-column > section:has(> a2ui-row > section:has(> a2ui-text)):not(:has(> a2ui-text))'),
    true,
    '自测锚失效：hasNestedHas 连上一版那条真嵌套式都认不出来，那这条护栏是空的'
  )
  assert.equal(
    hasNestedHas('.chat-a2ui-surface a2ui-column > section:has(> a2ui-row):not(:has(> a2ui-text)):not(:has(> a2ui-row > .a2ui-btn-host))'),
    false,
    '自测锚失效：hasNestedHas 把现役的非嵌套写法误判成嵌套'
  )
  // After stripping comments, split on { : the leading half of each chunk is that rule's selector (this file has no @media, so blocks never nest)
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const offenders = stripped
    .split('{')
    .slice(0, -1)
    .map((chunk) => chunk.slice(chunk.lastIndexOf('}') + 1).trim())
    .filter((sel) => hasNestedHas(sel))
  assert.deepEqual(offenders, [], `这些选择器把 :has() 套进了 :has()，Chrome 134 会整条静默丢弃：${JSON.stringify(offenders)}`)
})

test('护栏：容器前缀在全部十四条规则里逐字一致（共用前缀，改一条必须改全部十四条）', () => {
  const PREFIX = '.chat-a2ui-surface a2ui-column > section:has(> a2ui-row):not(:has(> a2ui-text)):not(:has(> a2ui-row > .a2ui-btn-host))'
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const n = stripped.split(PREFIX).length - 1
  assert.equal(n, 14, `容器前缀必须恰好出现在 14 条规则里（观感 / 带护栏的轨道 / 带护栏的拍平两条 / 格子度量 / 左衬 / 右衬 / 末行去边 / 宽表六条，实测 ${n} 次）：漏改任何一条，规格表就半新半旧`)
  // Positive anchor: the count is not an empty artifact — every prefix occurrence must sit at a rule-selector
  // position. Two legal shapes: (a) the chunk contains a `{`: it is a continuation segment (` > a2ui-row`,
  // ` a2ui-text:first-child`, etc.); before the `{` it must not contain `}` or `;` (if it does, it was spliced
  // into a declaration body/property value), and the rule body must be non-empty; (b) the chunk contains no
  // `{`: it may only be an entry of a comma-separated selector list (the first half of the flatten rule, whose
  // `{` comes after the next prefix occurrence), and the chunk must end with a comma. If the prefix is deleted
  // wholesale, n=0 is killed by the equal above.
  const bodies = stripped.split(PREFIX).slice(1)
  assert.ok(bodies.length === 14 && bodies.every(p => {
    const brace = p.indexOf('{')
    if (brace < 0) return p.trimEnd().endsWith(',')
    const head = p.slice(0, brace)
    const body = p.slice(brace + 1)
    return !head.includes('}') && !head.includes(';') && body.trimStart().length > 0 && body.includes('}')
  }), `14 处前缀必须每处都处在规则选择器位置（规则头或逗号列表项）、后跟非空规则体（实测 ${bodies.length} 处）`)
  assert.ok(css.includes('display: contents'), '拍平规则必须仍在文件里，前缀计数才有意义')
})

test('规格表：列宽由 grid 轨道算（最宽标签 max-content + 说明列下限 50%），74px 钉宽不许复活', () => {
  // This round's live regression: a long CJK label like "group 1, item gender" was squeezed onto two lines by
  // the 74px pinned width (session 5bd01b9b measured the first column at 74×46px). The alignment mechanism
  // moved from "pin a constant" to "share tracks", so this test is half negative assertion (the pinned width
  // must not come back) and half positive anchors (tracks, flattening and appearance must be present) — with
  // only negative assertions, deleting the entire section would still pass green.
  assert.doesNotMatch(css, /width:\s*74px/, '74px 钉宽已删：长标签（"第一组 1. 性别""条目数 800,000+"）会被挤成细条折行')
  assert.doesNotMatch(css, /a2ui-text:first-child\s*\{[^}]*flex:\s*none/, '标签列不许 flex:none —— 那是配合钉宽的，轨道布局下它会绑架列宽')
  // Positive anchor 1: container tracks. auto = the max-content of the widest label across the whole table
  // (cross-row sharing is innate to grid tracks); minmax(50%, 1fr) = the value column takes at least half, so
  // however long a label gets it only wraps and can never starve the value column.
  assert.match(css, /grid-template-columns:\s*auto\s+minmax\(50%,\s*1fr\)/, '容器必须是 auto + minmax(50%, 1fr) 两列轨道')
  // Positive anchor 2: row-box flattening. Unless the rows (a2ui-row and the in-row section) are flattened, the cells never hang on the tracks and sharing is out of the question.
  assert.match(css, /a2ui-row\s*,\s*\n\.chat-a2ui-surface/, '拍平规则的选择器列表必须先 a2ui-row 再行内 section（两条共用 display:contents）')
  assert.match(css, /a2ui-row\s*>\s*section\s*\{\s*display:\s*contents/, '行内布局 section 必须 display:contents 拍平')
  // Positive anchor 3: the dividers/padding have moved to the cells (the row box is gone; whatever was painted on the row disappears with it).
  assert.match(css, /a2ui-column\s*>\s*section:has[^{]*\ba2ui-text\s*\{[^}]*border-bottom:\s*1px solid var\(--td-component-border\)/, '表格内分隔线必须画在格子（a2ui-text）上')
  // Positive anchor 4: the label appearance was not swept away by the "delete the pinned width while you're at it" edit.
  assert.match(css, /a2ui-text:first-child\s*\{[^}]*color:\s*var\(--td-text-color-secondary\)/, '标签列必须用次级字色（参照卡 .k 的 --ink-3 muted）')
  assert.match(css, /a2ui-text:first-child\s*\{[^}]*font-size:\s*var\(--app-text-sm\)/, '标签列必须 --app-text-sm(12px) 小一号字（参照卡 .k 的 12.5px）')
  // Positive anchor 5: the value-column fallback for stray rows outside a table is still there (flex is inert inside grid; stray rows still rely on it to take the remaining width).
  assert.match(css, /a2ui-text:not\(:first-child\)\s*\{[^}]*flex:\s*1[^}]*min-width:\s*0/, '说明列必须 flex:1 + min-width:0 吃掉剩余宽度并允许长文换行')
})

test('参照卡无斜体：模型把目标行/标签行写成的 em 必须扳回正体（且要压得住宿主 markdown 那条）', () => {
  // The broad form alone is not enough: AnswerContent's scoped styles carry
  // `.answer-event .answer-content.markdown-content[data-v-*] em` at 0-4-1, which outright crushes the broad
  // form's 0-1-2 — the previous version had only the broad form, the live computed style stayed italic, and
  // this test was green. So we pin both the broad form (it still applies when a card is rendered outside the
  // chat body) and the same-chain high-specificity form (0-4-2).
  assert.match(css, /\.chat-a2ui-surface\s+a2ui-card\s+em\s*,/, '宽写法必须留在选择器列表里：卡片在分享页/预览等宿主 markdown 规则不在场的场合靠它')
  assert.match(
    css,
    /\.answer-event\s+\.answer-content\.markdown-content\s+\.chat-a2ui-surface\s+a2ui-card\s+em\s*\{\s*font-style:\s*normal;?\s*\}/,
    '必须另有一条沿 .answer-event > .answer-content.markdown-content > .chat-a2ui-surface 链、特异度 0-4-2 的写法，否则宿主那条 0-4-1 的斜体会赢'
  )
  assert.doesNotMatch(css, /font-style:[^;}]*!important/, '去斜体靠特异度不靠 !important —— important 计数是 styleGuard 的棘轮')
  assert.doesNotMatch(css, /\[data-v-[0-9a-f]{8}\][^{]*em/, '不许写死 data-v 作用域哈希：它随那次 SFC 重编译变，写死就是定时炸弹')
})

test('按钮排与表格留 15px 间距（参照卡 .card-actions 的 margin-top:15px）', () => {
  assert.match(css, /a2ui-row\s*>\s*section:has\(>\s*\.a2ui-btn-host\)\s*\{\s*margin-top:\s*15px;?\s*\}/, '按钮排必须 :has(> .a2ui-btn-host) 锁定后 margin-top:15px，否则按钮贴住表格底边')
})

test('宽表分支：任一行 ≥3 格走 classic 表格布局；轨道与拍平必须挂护栏让位', () => {
  // Live root cause: session 1541affe, "demo table: 4 columns × 5 rows" — .18's 2-column tracks flattened
  // the 4-cell rows and flowed them row-first into two tracks, tearing one logical row into two visual rows.
  // Wide tables take the display:table family instead (row = row-group, in-row section = table-row,
  // cell = table-cell): cross-row column-width sharing is innate to table layout and holds for any column
  // count and for rows of uneven length. Every number below is a literal fixed by measuring the landed bytes.
  const WD = ':has(> a2ui-row > a2ui-text:nth-child(3), > a2ui-row > section > a2ui-text:nth-child(3))'
  // Write the GUARD string whole as a literal; ':not(' + WD + ')' concatenation is forbidden: the previous
  // version assembled ':not' + WD, which happened to reproduce the exact broken shape of the CSS patch's
  // missing parenthesis (:not:has(...) is invalid and the browser silently drops the whole rule), and the
  // same-source symbol let the broken patch pass on both sides. A literal shares no symbol with the
  // implementation, so breaking either side goes red.
  const GUARD = ':not(:has(> a2ui-row > a2ui-text:nth-child(3), > a2ui-row > section > a2ui-text:nth-child(3)))'
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, '')
  // Whole-file invariant (independent backstop, depends on none of the constants above): the functional pseudo-class :not must be immediately followed by (.
  assert.equal(stripped.split(':not').length - 1, stripped.split(':not(').length - 1, `每个 :not 后面必须紧跟 ( —— 漏括号写成 :not:has(...) 时整条规则被浏览器静默丢弃，而按字符串数数的护栏计数照样全绿（2026-11-06 真栽过一次）`)
  // The detector appears at 9 sites = 6 wide-table rules + 3 guardrail sites (the GUARD literal embeds WD, so they are counted together).
  // Removing any guardrail or deleting the whole wide-table branch must move this number (count invariant: removing a guard must be visible).
  assert.equal(stripped.split(WD).length - 1, 9, `WD 检测器必须共 9 处（宽表规则 6 + 护栏 3），实测 ${stripped.split(WD).length - 1} 次`)
  assert.equal(stripped.split(GUARD).length - 1, 3, `护栏必须恰好 3 处（轨道规则 1 + 拍平规则 2），实测 ${stripped.split(GUARD).length - 1} 次 —— 漏挂任何一处，宽表又会被 2 列轨道撕行`)
  // Positive anchor: the selectors of the three track/flatten rules really do end with the guard (not just guard text cheering from elsewhere).
  const g = GUARD.replace(/[()*+?.[\]^${}|\\]/g, '\\$&')
  assert.ok(new RegExp(g + '\\s*\\{[^}]*display:\\s*grid').test(stripped), 'display:grid 那条轨道规则必须以护栏收尾')
  assert.ok(new RegExp(g + '\\s*>\\s*a2ui-row,').test(stripped), '拍平规则第一项（a2ui-row）必须以护栏收尾')
  assert.ok(new RegExp(g + '\\s*>\\s*a2ui-row\\s*>\\s*section\\s*\\{').test(stripped), '拍平规则第二项（行内 section）必须以护栏收尾')
  // Positive anchor: the wide-table box tree gets a real box at each of its four layers, zero display:contents.
  assert.match(css, /display:\s*table;[\s\S]{0,80}?width:\s*100%;[\s\S]{0,80}?border-spacing:\s*0;/, '宽表容器 display:table + width:100% + border-spacing:0 三条必须在同一条规则里')
  assert.match(css, /display:\s*table-row-group;/, '宽表的 a2ui-row 必须是 table-row-group（真盒，不赌 contents）')
  assert.match(css, /display:\s*table-row;/, '宽表行内布局 section 必须是 table-row')
  assert.match(css, /display:\s*table-cell;/, '宽表格子必须是 table-cell')
  // Positive anchor: 6+6 cell padding reproduces the column-gap look + long-token overflow protection.
  assert.match(css, /display:\s*table-cell;[^}]*vertical-align:\s*top;[^}]*word-break:\s*break-word;[^}]*padding-left:\s*6px;[^}]*padding-right:\s*6px/, '宽表格子必须 vertical-align:top + word-break + 左右 6px 内衬')
  // The negative assertions are paired with the positive anchors above: deleting only the wide-table branch turns both the count and the display assertions red.
})
