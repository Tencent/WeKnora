/**
 * Strip ingestion noise from chunk text before display: YAML frontmatter,
 * markdown footnote reference markers (e.g. `^(\[4\])`, `[^4]`, `[4]`) and
 * stray backslash escapes. Keeps human-readable prose intact.
 */
function stripNoise(text: string): string {
  let s = text || '';

  // Leading YAML frontmatter block: --- ... ---
  s = s.replace(/^\uFEFF?\s*---[\s\S]*?\n---\s*/, '');

  // Inline frontmatter leftovers dragged into a single-line snippet
  s = s.replace(
    /-{2,}\s*((?:title|author|url|date|tags?|description)\s*:[\s\S]*?)-{2,}/gi,
    ' ',
  );

  // Footnote / citation markers: ^(\[4\]) / ^[4] / [^4] / [4]
  s = s.replace(/\^?\(?\s*\\?\[\s*\^?\d+\s*\\?\]\s*\)?/g, '');

  // Unescape common markdown backslash escapes
  s = s.replace(/\\([\\[\]()*_#`~\-+.!>])/g, '$1');

  return s;
}

/** Clean multi-line content for full display, preserving paragraph breaks. */
export function cleanContent(text: string): string {
  return stripNoise(text)
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

/** Clean a snippet down to a single, whitespace-collapsed line. */
export function cleanSnippet(text: string): string {
  return stripNoise(text).replace(/\s+/g, ' ').trim();
}
