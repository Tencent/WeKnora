/**
 * Styles for the standalone conversation HTML export.
 *
 * This file is intentionally independent from the app's chat stylesheets
 * (`chat-markdown.less`, `chat-citations.less`, `chat-message-shared.less`).
 * Those are Less mixins consumed inside `<style scoped>` blocks, so their
 * selectors are compiled with Vue scope attributes (`[data-v-*]`) that the
 * `v-html` output does not carry — none of them can style a detached document.
 *
 * The exported document must also survive constraints the app stylesheet never
 * had: opened from `file://`, no JavaScript, no app CSS custom properties, and
 * printed to PDF. So the rules below are written against literal light-theme
 * values, use `.export-content` as their root, and carry explicit print
 * overrides for the horizontally scrollable blocks (tables, code, mermaid).
 *
 * The class names targeted here are the contract of `renderChatMarkdown()`;
 * `sessionHtmlExport.test.ts` fails if that contract grows a new class.
 */

export interface ExportCssOptions {
  /** Resolved sans-serif stack, baked in so the file renders the same offline. */
  fontFamily: string
  /** Resolved monospace stack. */
  fontFamilyMono: string
  /** KaTeX stylesheet, already rewritten to standalone-safe font URLs. */
  katexCss?: string
  /** highlight.js light theme. */
  hljsCss?: string
}

const baseCss = (sansStack: string, monoStack: string): string => `:root{
--td-text-color-primary:rgba(0,0,0,.9);
--td-text-color-secondary:rgba(0,0,0,.6);
--td-text-color-placeholder:rgba(0,0,0,.4);
--td-bg-color-container:#fff;
--td-bg-color-secondarycontainer:#f3f3f3;
--td-component-stroke:#e7e7e7;
--td-component-border:#dcdcdc;
--td-brand-color:#07c05f;
--td-brand-color-hover:#08dd6e;
--td-error-color:#e34d59;
--export-font-sans:${sansStack};
--export-font-mono:${monoStack};
}
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{
background:#f5f6f7;
color:var(--td-text-color-primary);
font-family:var(--export-font-sans);
font-size:16px;
line-height:1.625;
-webkit-font-smoothing:antialiased;
}
.export-page{
max-width:860px;
margin:0 auto;
padding:32px 40px 56px;
background:var(--td-bg-color-container);
border:1px solid var(--td-component-stroke);
border-radius:12px;
}
.export-header{padding-bottom:20px;border-bottom:1px solid var(--td-component-stroke)}
.export-header h1{margin:0 0 8px;font-size:24px;line-height:1.35;word-break:break-word}
.export-meta{margin:0;font-size:13px;color:var(--td-text-color-secondary);word-break:break-all}
.export-message{margin-top:28px}
.export-message__role{
margin-bottom:8px;
font-size:12px;
font-weight:600;
letter-spacing:.06em;
text-transform:uppercase;
color:var(--td-text-color-secondary);
}
.export-message--user .export-user-text{
white-space:pre-wrap;
word-break:break-word;
background:var(--td-bg-color-secondarycontainer);
border-radius:10px;
padding:12px 16px;
}
.export-message__sub{margin-top:14px;font-size:13px;color:var(--td-text-color-secondary)}
.export-message__sub-title{margin:0 0 4px;font-weight:600}
.export-message__sub ul{margin:0;padding-left:1.4em}
.export-content{font-size:16px;line-height:1.625;color:var(--td-text-color-primary);word-break:break-word}
.export-content h1,.export-content h2,.export-content h3,
.export-content h4,.export-content h5,.export-content h6{
margin:1.1em 0 .5em;
font-weight:600;
line-height:1.4;
}
.export-content h1{font-size:1.5em}
.export-content h2{font-size:1.3em}
.export-content h3{font-size:1.15em}
.export-content h4,.export-content h5,.export-content h6{font-size:1em}
.export-content p{margin:0 0 .25em}
.export-content p+p{margin-top:.75em}
.export-content ul,.export-content ol{margin:.5em 0;padding-left:1.6em}
.export-content li{margin:.2em 0}
.export-content blockquote{
margin:.75em 0;
padding:.25em 0 .25em 1em;
border-left:3px solid var(--td-component-stroke);
color:var(--td-text-color-secondary);
}
.export-content a{color:var(--td-brand-color);text-decoration:underline;text-underline-offset:2px}
.export-content strong{font-weight:600}
.export-content .md-strong-title{margin-top:1em;font-weight:600}
.export-content hr{margin:1.5em 0;border:0;border-top:1px solid var(--td-component-stroke)}
.export-content code{
font-family:var(--export-font-mono);
font-size:.875em;
background:var(--td-bg-color-secondarycontainer);
border:1px solid var(--td-component-stroke);
border-radius:4px;
padding:.1em .35em;
}
.export-content img{max-width:100%;height:auto;border-radius:8px;display:block;margin:1.25em 0}
.export-content .export-image-missing{
display:inline-block;
margin:1em 0;
padding:4px 10px;
border:1px dashed var(--td-component-border);
border-radius:6px;
font-size:13px;
color:var(--td-text-color-secondary);
}
/* Fenced code blocks */
.export-content .chat-code-block{
margin:.875em 0;
border:1px solid var(--td-component-stroke);
border-radius:8px;
overflow:hidden;
background:var(--td-bg-color-container);
}
.export-content .chat-code-block__header{
display:flex;
align-items:center;
min-height:32px;
padding:4px 12px;
background:var(--td-bg-color-secondarycontainer);
border-bottom:1px solid var(--td-component-stroke);
font-size:12px;
color:var(--td-text-color-secondary);
}
.export-content .chat-code-block__lang{font-weight:600;letter-spacing:.04em;text-transform:uppercase}
.export-content .chat-code-block__pre{
margin:0;
padding:14px 16px;
background:var(--td-bg-color-secondarycontainer);
overflow-x:auto;
font-size:13px;
line-height:1.55;
}
.export-content .chat-code-block__pre code{
display:block;
padding:0;
border:0;
border-radius:0;
background:transparent;
font-size:inherit;
font-weight:400;
white-space:pre;
}
.export-content .hljs{background:transparent}
/* Mermaid */
.export-content .chat-mermaid-block{
margin:.75em 0;
border:1px solid var(--td-component-stroke);
border-radius:10px;
overflow:hidden;
background:var(--td-bg-color-container);
}
.export-content .chat-mermaid-block__header{
display:flex;
align-items:center;
min-height:32px;
padding:4px 12px;
background:var(--td-bg-color-secondarycontainer);
border-bottom:1px solid var(--td-component-stroke);
}
.export-content .chat-mermaid-block__badge{
font-size:12px;
font-weight:600;
letter-spacing:.04em;
text-transform:uppercase;
color:var(--td-text-color-secondary);
}
.export-content .chat-mermaid-block__canvas{
margin:0;
padding:20px 16px 18px;
border:0;
background:var(--td-bg-color-container);
overflow-x:auto;
text-align:center;
}
.export-content .chat-mermaid-block__canvas svg{max-width:100%;height:auto}
/* Tables: the wrapper scrolls, the cells do not wrap. Both halves are required;
   print overrides below turn the same table back into a wrapping full-width one. */
.export-content .chat-markdown-table{
width:fit-content;
max-width:100%;
margin:.875em 0 1em;
overflow-x:auto;
border:1px solid var(--td-component-stroke);
border-radius:8px;
background:var(--td-bg-color-container);
-webkit-overflow-scrolling:touch;
}
.export-content table{
display:table;
width:max-content;
min-width:0;
border-collapse:separate;
border-spacing:0;
font-size:14px;
line-height:1.55;
word-break:initial;
}
.export-content table thead{background-color:var(--td-bg-color-secondarycontainer)}
.export-content table tbody tr:nth-child(2n){background-color:rgba(0,0,0,.02)}
.export-content table tr th{
margin:0;
padding:8px 12px;
border:0;
border-right:1px solid var(--td-component-stroke);
border-bottom:1px solid var(--td-component-stroke);
background-color:var(--td-bg-color-secondarycontainer);
font-weight:600;
text-align:left;
white-space:nowrap;
}
.export-content table tr td{
margin:0;
padding:8px 12px;
border:0;
border-right:1px solid var(--td-component-stroke);
border-bottom:1px solid var(--td-component-stroke);
text-align:left;
vertical-align:top;
}
.export-content table tr th:last-child,
.export-content table tr td:last-child{border-right:0}
.export-content table tbody tr:last-child td{border-bottom:0}
.export-content table th[align='center'],
.export-content table td[align='center']{text-align:center}
.export-content table th[align='right'],
.export-content table td[align='right']{text-align:right}
/* Citations. The tooltip is emitted inline by the renderer and is hidden here
   only — without this rule every citation leaks its title and URL. */
.export-content .citation{
display:inline-flex;
align-items:center;
gap:2px;
box-sizing:border-box;
margin:0 .06em;
padding:0 5px;
border-radius:999px;
font-size:.72em;
line-height:1.45;
vertical-align:baseline;
transform:translateY(-.04em);
}
.export-content .citation-kb{
background:rgba(0,0,0,.04);
color:rgba(0,0,0,.86);
box-shadow:inset 0 0 0 1px rgba(0,0,0,.1);
white-space:nowrap;
}
.export-content .citation-web{
background:rgba(7,192,95,.08);
color:#057a3d;
box-shadow:inset 0 0 0 1px rgba(7,192,95,.2);
text-decoration:none;
white-space:nowrap;
}
.export-content .citation-text,
.export-content .citation-domain{
max-width:180px;
overflow:hidden;
line-height:1.45;
text-overflow:ellipsis;
}
.export-content .citation-tip{display:none}
.export-content a.wiki-content-link{
color:var(--td-brand-color);
border-bottom:1px dashed var(--td-brand-color);
font-weight:500;
text-decoration:none;
}
/* Print: scrolling containers clip their overflow on paper, which silently
   drops the right half of a wide table or the tail of a long code line. */
@media print{
body{background:#fff}
.export-page{max-width:none;margin:0;padding:0;border:0;border-radius:0}
.export-content .chat-markdown-table,
.export-content .chat-code-block__pre,
.export-content .chat-mermaid-block__canvas{overflow:visible!important}
.export-content table{width:100%!important;table-layout:fixed}
.export-content table tr th{white-space:normal!important}
.export-content .chat-code-block__pre code{white-space:pre-wrap!important;word-break:break-word}
}`

function escapeForCssValue(value: string): string {
  // Keep quotes: font stacks legitimately contain them (`"Segoe UI"`). Only
  // remove what could break out of the declaration or the `<style>` element.
  return String(value || '').replace(/[<>;{}]/g, '').replace(/[\u0000-\u001f]/g, ' ').trim()
}

/**
 * Build the full `<style>` payload for an exported document.
 *
 * `katexCss` / `hljsCss` are only passed in when the rendered body actually
 * uses them, so an export without math or code does not pay their size.
 */
export function buildExportCss(options: ExportCssOptions): string {
  const parts = [
    baseCss(escapeForCssValue(options.fontFamily), escapeForCssValue(options.fontFamilyMono)),
  ]
  if (options.hljsCss) parts.push(options.hljsCss)
  if (options.katexCss) parts.push(options.katexCss)
  return parts.join('\n')
}
