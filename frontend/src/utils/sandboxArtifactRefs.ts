/**
 * 把回答正文里对「沙箱生成文件」的引用，接到 artifact 下载链路上。
 *
 * 模型会用 Markdown 图片语法引用它在沙箱里生成的文件（提示词规定写成
 * `![说明](sandbox:文件名)`），服务端在落库前把它改写成该文件的稳定句柄
 * `resource://<handle>` —— 与知识库图片、聊天附件同一种引用形式。两种写法
 * 都指向同一份 `Message.Artifacts`：
 *
 *   - `resource://<handle>` —— 落库后的权威形式，历史会话读到的就是它；
 *   - `sandbox:<文件名>`    —— 模型原始写法，本轮流式输出尚未改写时用到。
 *
 * 由于句柄形式与知识库图片完全一致，一条回答里同时出现「检索到的知识库图」
 * 和「技能生成的图」是常态。所以句柄对不上本消息产物列表时必须返回 null，
 * 交回默认的受保护图片渲染，而不是显示「文件不可用」。
 *
 * 图片类产物内联显示（带鉴权拉取后换成 blob），其余类型（HTML 图表、CSV、
 * 文档等）渲染成一张卡片，点击后交给 ChatArtifactsDrawer 预览——正文里塞一个
 * 1MB 的自包含 HTML iframe 既慢又不安全。
 */

import { escapeHTML } from './security.ts';

/** 与后端 artifactListItem / SSE publicArtifactViews 对齐的最小字段集。 */
export interface ArtifactRefMeta {
  index: number;
  file_name: string;
  file_type?: string;
  /** `resource://<handle>`。后端未启用资源目录时为空，此时只能按文件名解析。 */
  handle?: string;
  /**
   * 历史消息直接携带 `Message.Artifacts`，其中的存储引用叫 `url`。与 handle
   * 同义，取其一即可。
   */
  url?: string;
}

export interface ArtifactRefContext {
  sessionId: string;
  messageId: string;
}

export interface ArtifactRefLabels {
  /** 卡片副标题，如「点击预览」。 */
  previewHint: string;
  /** 本轮已结束但引用对不上任何产物时的副标题，如「文件不可用」。 */
  missingHint: string;
}

const RESOURCE_HANDLE_RE = /^resource:\/\/([A-Za-z0-9_-]{22})$/;
const SANDBOX_NAME_RE = /^sandbox:(?:\/\/)?(.+)$/i;
const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'avif']);
const TRANSPARENT_PIXEL =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';

type ArtifactRef = { kind: 'handle'; handle: string } | { kind: 'name'; name: string };

function parseArtifactRef(href: string): ArtifactRef | null {
  const trimmed = (href || '').trim();
  if (!trimmed) return null;

  const handleMatch = trimmed.match(RESOURCE_HANDLE_RE);
  if (handleMatch) {
    return { kind: 'handle', handle: handleMatch[1] };
  }

  const nameMatch = trimmed.match(SANDBOX_NAME_RE);
  if (!nameMatch) return null;
  let name = nameMatch[1].trim();
  try {
    name = decodeURIComponent(name);
  } catch {
    // 保留原样：文件名里出现裸 % 时 decodeURIComponent 会抛错。
  }
  // 目录前缀不带信息量，产物是按文件名索引的。
  name = name.split('/').pop() || '';
  return name ? { kind: 'name', name } : null;
}

/**
 * 该链接目标是否可能是沙箱产物引用。
 *
 * 句柄形式与知识库图片同形，因此这里为真只说明「值得交给产物解析试一次」，
 * 不代表本消息真有这个文件。
 */
export function isArtifactRefHref(href: string): boolean {
  return parseArtifactRef(href) !== null;
}

function artifactHandle(artifact: ArtifactRefMeta): string {
  const raw = (artifact.handle || artifact.url || '').trim();
  return raw.match(RESOURCE_HANDLE_RE)?.[1] || '';
}

const CODE_SPAN_OR_FENCE_RE = /(```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`)/g;

/**
 * 扫描一个 Markdown 链接目标，返回括号内的原文与右括号位置。
 *
 * 用括号配对而不是正则，是因为技能生成的文件名经常带括号和空格
 * （`腾讯控股(00700) 成交量_838ccc.html`）。marked 会在第一个空格处把目标截断，
 * 于是引用永远匹配不上产物；而按深度扫描能准确找到真正闭合链接的那个右括号。
 */
function scanLinkDestination(text: string, openIndex: number): { inner: string; end: number } | null {
  let depth = 1;
  for (let i = openIndex + 1; i < text.length; i += 1) {
    const ch = text[i];
    if (ch === '\n') return null;
    if (ch === '(') depth += 1;
    else if (ch === ')') {
      depth -= 1;
      if (depth === 0) return { inner: text.slice(openIndex + 1, i), end: i };
    }
  }
  return null;
}

/** 把目标里可选的标题部分（`dest "title"`）拆出来。 */
function splitDestinationTitle(inner: string): { destination: string; title: string } {
  const match = inner.match(/^([\s\S]*?)(\s+(?:"[^"]*"|'[^']*'))$/);
  if (!match) return { destination: inner.trim(), title: '' };
  return { destination: match[1].trim(), title: match[2] };
}

/** `](` 之前必须是同一行上闭合的 `[...]`，否则这不是一个链接。 */
function hasLinkLabelBefore(text: string, closeBracketIndex: number): boolean {
  for (let i = closeBracketIndex - 1; i >= 0; i -= 1) {
    const ch = text[i];
    if (ch === '\n') return false;
    if (ch === '[') return true;
  }
  return false;
}

function normalizeSegment(segment: string): string {
  if (!segment.includes('](')) return segment;

  let out = '';
  let cursor = 0;
  while (cursor < segment.length) {
    const relative = segment.slice(cursor).indexOf('](');
    if (relative < 0) break;
    const closeBracket = cursor + relative;
    const open = closeBracket + 1;

    if (!hasLinkLabelBefore(segment, closeBracket)) {
      out += segment.slice(cursor, open + 1);
      cursor = open + 1;
      continue;
    }
    const scanned = scanLinkDestination(segment, open);
    if (!scanned) {
      out += segment.slice(cursor, open + 1);
      cursor = open + 1;
      continue;
    }

    const { destination, title } = splitDestinationTitle(scanned.inner);
    const ref = parseArtifactRef(destination);
    if (!ref || ref.kind !== 'name') {
      out += segment.slice(cursor, scanned.end + 1);
      cursor = scanned.end + 1;
      continue;
    }

    // Percent-encode so marked sees a single whitespace-free token;
    // parseArtifactRef decodes it again on the way out.
    out += `${segment.slice(cursor, open + 1)}sandbox:${encodeURIComponent(ref.name)}${title})`;
    cursor = scanned.end + 1;
  }
  return out + segment.slice(cursor);
}

/**
 * 在 marked 解析前，把 `sandbox:` 引用的目标规整成不含空格的单个 token。
 *
 * 不做这一步的话，带空格的文件名会被 marked 从空格处切开，目标只剩前半截，
 * 后半截漏成正文——正是「卡片名字被截断 + 尾巴变成裸文本」那个现象。
 * 代码块内的示例原样保留。
 */
export function normalizeSandboxArtifactRefs(markdown: string): string {
  if (!markdown || !markdown.includes('](')) return markdown;
  if (!/\]\(\s*sandbox:/i.test(markdown)) return markdown;

  const parts = markdown.split(CODE_SPAN_OR_FENCE_RE);
  for (let i = 0; i < parts.length; i += 2) {
    parts[i] = normalizeSegment(parts[i]);
  }
  return parts.join('');
}

/** 把引用解析成具体产物；解析不到（尚未收集完/文件名对不上）返回 null。 */
export function resolveArtifactRef(
  href: string,
  artifacts: ArtifactRefMeta[] | undefined | null,
): ArtifactRefMeta | null {
  const ref = parseArtifactRef(href);
  if (!ref || !artifacts?.length) return null;

  if (ref.kind === 'handle') {
    return artifacts.find((item) => artifactHandle(item) === ref.handle) || null;
  }
  return artifacts.find((item) => (item.file_name || '').trim() === ref.name) || null;
}

function fileExtension(fileName: string): string {
  const base = (fileName || '').trim().toLowerCase();
  const dot = base.lastIndexOf('.');
  return dot > 0 ? base.slice(dot + 1) : '';
}

/**
 * 是否按图片内联渲染。SVG 刻意排除：它是可执行内容，走卡片 + 预览的沙箱路径。
 */
function rendersAsImage(artifact: ArtifactRefMeta): boolean {
  const ext = fileExtension(artifact.file_name);
  if (ext) return IMAGE_EXTENSIONS.has(ext);
  const type = (artifact.file_type || '').toLowerCase();
  return type.startsWith('image/') && !type.includes('svg');
}

// blob URL 按 (会话, 消息, 下标) 缓存，并挂在 window 上：Vite 热更新会替换模块
// 但不会重建文档，模块级 Map 会让已加载的图片退回占位图。
type ArtifactBlobState = { blobByKey: Map<string, string>; inflight: Map<string, Promise<string | null>> };

const artifactBlobState: ArtifactBlobState = (() => {
  const fresh = (): ArtifactBlobState => ({ blobByKey: new Map(), inflight: new Map() });
  if (typeof window === 'undefined') return fresh();
  const scope = window as typeof window & { __weknoraArtifactBlobCacheV1__?: ArtifactBlobState };
  scope.__weknoraArtifactBlobCacheV1__ ||= fresh();
  return scope.__weknoraArtifactBlobCacheV1__;
})();

function blobCacheKey(ctx: ArtifactRefContext, index: number): string {
  return `${ctx.sessionId}\u0000${ctx.messageId}\u0000${index}`;
}

function fileIconSvg(): string {
  return (
    '<svg class="artifact-ref-card__glyph" viewBox="0 0 24 24" aria-hidden="true">'
    + '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6z" '
    + 'fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/>'
    + '<path d="M14 2v6h6" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/>'
    + '</svg>'
  );
}

// 与 chatMarkdownRenderer 的流式图片骨架同一个类名，样式复用。
const STREAMING_PLACEHOLDER =
  '<span class="streaming-image-loading"><span class="streaming-image-loading__skeleton"></span></span>';

function renderCard(fileName: string, hint: string, index: number | null): string {
  const safeName = escapeHTML(fileName);
  const safeHint = escapeHTML(hint);
  // 卡片必须是内联元素：marked 会把图片包在 <p> 里，块级元素会被 HTML 解析器
  // 提到段落外面，破坏正文结构。
  const interactive = index === null
    ? ''
    : ` data-artifact-index="${index}" role="button" tabindex="0"`;
  const state = index === null ? ' artifact-ref-card--pending' : '';
  return (
    `<span class="artifact-ref-card${state}"${interactive} title="${safeName}">`
    + `<span class="artifact-ref-card__icon" aria-hidden="true">${fileIconSvg()}</span>`
    + '<span class="artifact-ref-card__text">'
    + `<span class="artifact-ref-card__name">${safeName}</span>`
    + `<span class="artifact-ref-card__hint">${safeHint}</span>`
    + '</span></span>'
  );
}

function renderImage(
  artifact: ArtifactRefMeta,
  alt: string,
  ctx: ArtifactRefContext | null,
): string {
  const safeAlt = escapeHTML(alt || artifact.file_name || '');
  // 已经拉取过就直接给 blob：流式重渲染会重建 <img>，否则每帧都会闪回占位图。
  const cached = ctx ? artifactBlobState.blobByKey.get(blobCacheKey(ctx, artifact.index)) : undefined;
  const src = cached || TRANSPARENT_PIXEL;
  const loading = cached ? '' : ' data-img-loading="1"';
  return (
    `<img class="markdown-image artifact-ref-image" src="${src}" alt="${safeAlt}"`
    + ` data-artifact-index="${artifact.index}"${loading}>`
  );
}

/**
 * 渲染一个 Markdown 图片/链接目标。
 *
 * 返回 null 表示这不是沙箱产物引用，调用方应回落到默认渲染（普通图片、
 * `resource://` 受保护图片、外链等一律不受影响）。
 */
export function renderArtifactReference(args: {
  href: string;
  alt?: string;
  artifacts?: ArtifactRefMeta[] | null;
  labels: ArtifactRefLabels;
  context?: ArtifactRefContext | null;
  /** 本轮回答还在生成中。产物要到本轮结束才会收集，此时解析不到是正常的。 */
  streaming?: boolean;
}): string | null {
  const ref = parseArtifactRef(args.href);
  if (!ref) return null;

  const artifact = resolveArtifactRef(args.href, args.artifacts);
  if (!artifact) {
    // 句柄对不上本消息的产物，说明这是别的受保护文件（知识库检索图、
    // 附件图……）。交回默认渲染，由 hydrateProtectedFileImages 带鉴权拉取。
    if (ref.kind === 'handle') return null;
    // 产物列表要到本轮结束才随 complete 事件到达，流式期间必然解析不到。
    // 这时给骨架屏而不是卡片：既避免半截文件名闪一下，也不会把「生成中」
    // 这种状态留在一个其实已经结束的回答里。
    if (args.streaming) return STREAMING_PLACEHOLDER;
    // 本轮已结束仍对不上，说明模型引用了并不存在的文件。如实说明，不要
    // 继续显示「生成中」。
    const fallbackName = ref.name || (args.alt || '').trim();
    if (!fallbackName) return '';
    return renderCard(fallbackName, args.labels.missingHint, null);
  }

  if (rendersAsImage(artifact)) {
    return renderImage(artifact, args.alt || '', args.context ?? null);
  }
  return renderCard(artifact.file_name, args.labels.previewHint, artifact.index);
}

async function loadArtifactBlobURL(ctx: ArtifactRefContext, index: number): Promise<string | null> {
  const key = blobCacheKey(ctx, index);
  const cached = artifactBlobState.blobByKey.get(key);
  if (cached) return cached;

  let task = artifactBlobState.inflight.get(key);
  if (!task) {
    task = (async () => {
      try {
        // Imported lazily so parsing/rendering stays free of the axios
        // transport — those parts run in plain Node during unit tests.
        const { downloadArtifact } = await import('@/api/chat');
        const blob = await downloadArtifact(ctx.sessionId, ctx.messageId, index);
        const blobURL = URL.createObjectURL(blob);
        artifactBlobState.blobByKey.set(key, blobURL);
        return blobURL;
      } catch (error) {
        console.warn('[sandboxArtifactRefs] artifact image load failed:', error);
        return null;
      } finally {
        artifactBlobState.inflight.delete(key);
      }
    })();
    artifactBlobState.inflight.set(key, task);
  }
  return task;
}

/**
 * 把正文里的产物图片换成带鉴权拉取的 blob。
 *
 * 与 hydrateProtectedFileImages 同样是幂等的：已换过的元素带 authHydrated 标记，
 * 相同文件的并发请求共享同一个 Promise。
 */
export async function hydrateArtifactImages(
  root: ParentNode | null | undefined,
  ctx: ArtifactRefContext | null | undefined,
): Promise<void> {
  if (!root || !ctx?.sessionId || !ctx?.messageId) return;

  const images = Array.from(
    root.querySelectorAll<HTMLImageElement>('img.artifact-ref-image[data-artifact-index]'),
  ).filter((img) => img.dataset.authHydrated !== '1');
  if (!images.length) return;

  await Promise.all(images.map(async (img) => {
    const index = Number(img.getAttribute('data-artifact-index'));
    if (!Number.isInteger(index) || index < 0) return;
    img.dataset.authHydrated = '1';

    const blobURL = await loadArtifactBlobURL(ctx, index);
    if (!blobURL) {
      img.dataset.authHydrated = '0';
      return;
    }
    img.src = blobURL;
    img.removeAttribute('data-img-loading');
  }));
}

/**
 * 从点击/键盘事件里取出被激活的产物卡片下标；返回 null 表示事件与卡片无关。
 */
export function artifactIndexFromEventTarget(target: EventTarget | null): number | null {
  if (!(target instanceof Element)) return null;
  const card = target.closest('.artifact-ref-card[data-artifact-index]');
  if (!card) return null;
  const index = Number(card.getAttribute('data-artifact-index'));
  return Number.isInteger(index) && index >= 0 ? index : null;
}
