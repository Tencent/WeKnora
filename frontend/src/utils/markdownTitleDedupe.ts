/**
 * 去掉正文开头与文档标题重复的一级标题。
 *
 * 文档抽屉 / 预览工具栏本身已经展示了条目名，而不少导入来源（飞书、钉钉、
 * MarkItDown 等）会在正文第一行再写一次 `# 标题`，视觉上就是同一个标题连
 * 出现两次。这里只在「正文第一个非空行是 ATX 一级标题，且规范化后等于当前
 * 条目名」时删掉这一行；标题不同、没有一级标题、或一级标题出现在正文中间
 * 时都原样返回，避免误删真正的章节结构。
 *
 * 本模块不依赖 DOM / marked，纯字符串处理，便于单测覆盖。
 */

/** YAML Front matter（允许在去掉标题时原样保留）。 */
const FRONT_MATTER = /^---[ \t]*\r?\n[\s\S]*?\r?\n---[ \t]*\r?\n/;

/** 仅用于把「文件名」和「标题」对齐的常见文档扩展名。 */
const FILE_EXTENSION = /\.(?:md|markdown|mdown|mkd|mdx|txt|text|html?|pdf|docx?|pptx?)$/i;

const ZERO_WIDTH = /[\u200b-\u200d\u2060\ufeff]/g;

/**
 * 标题规范化：只抹平「展示层」差异，不做模糊匹配。
 *
 * - NFKC：全角字母/数字与半角等价（`２０２４` == `2024`）；
 * - 去掉零宽字符、折叠空白；
 * - 去掉标题/条目名开头常见的装饰标签（`【使用必看】`、`[Draft]`、`(1)`）；
 * - 去掉行内 Markdown 标记（`**`、`` ` ``、`[文字](链接)` 只留文字）；
 * - 去掉 ATX 标题两端的 `#`；
 * - ASCII 大小写不敏感。
 *
 * 规范化后仍不相等就不删，这是「宁可不删，不可误删」的边界。
 */
export function normalizeHeadingText(value: string): string {
  return value
    .normalize('NFKC')
    .replace(ZERO_WIDTH, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`~]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^(?:【[^】]*】|\[[^\]]*\]|\([^)]*\))\s*/, '')
    .replace(/^#+\s*/, '')
    .replace(/\s*#+$/, '')
    .trim()
    .toLowerCase();
}

function titleCandidates(titles: Array<string | null | undefined>): Set<string> {
  const candidates = new Set<string>();
  for (const title of titles) {
    if (typeof title !== 'string') continue;
    const trimmed = title.trim();
    if (!trimmed) continue;
    const withoutExtension = trimmed.replace(FILE_EXTENSION, '');
    for (const variant of [trimmed, withoutExtension]) {
      const normalized = normalizeHeadingText(variant);
      if (normalized) candidates.add(normalized);
    }
  }
  return candidates;
}

/**
 * 删除正文开头与 `titles` 之一重复的一级标题。
 *
 * @param markdown 原始 Markdown 文本（可带 Front matter）。
 * @param titles 当前界面上已经展示过的标题 / 文件名；空值会被忽略。
 * @returns 去掉重复标题后的 Markdown；未命中时返回与输入完全相同的字符串。
 */
export function stripDuplicateLeadingH1(
  markdown: string,
  titles: Array<string | null | undefined>,
): string {
  if (!markdown || typeof markdown !== 'string') return markdown;
  const candidates = titleCandidates(titles);
  if (candidates.size === 0) return markdown;

  const frontMatter = markdown.match(FRONT_MATTER)?.[0] ?? '';
  const body = markdown.slice(frontMatter.length);
  const eol = body.includes('\r\n') ? '\r\n' : '\n';
  const lines = body.split(/\r?\n/);

  let firstContent = 0;
  while (firstContent < lines.length && lines[firstContent].trim() === '') firstContent += 1;
  if (firstContent >= lines.length) return markdown;

  // 只认 ATX 一级标题（`#` + 空格），`##` 及以下保持不动。
  const heading = lines[firstContent].match(/^[ \t]{0,3}#(?:[ \t]+(.*))?$/);
  if (!heading) return markdown;
  if (!candidates.has(normalizeHeadingText(heading[1] ?? ''))) return markdown;

  // 连同标题行后面紧跟的空行一起删掉，避免正文顶部多出空行。
  let removeEnd = firstContent + 1;
  while (removeEnd < lines.length && lines[removeEnd].trim() === '') removeEnd += 1;

  return frontMatter + lines.slice(removeEnd).join(eol);
}
