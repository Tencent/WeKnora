/**
 * katexShared.ts — Pre-render + placeholder approach for KaTeX in streaming contexts.
 *
 * Problem: `marked-katex-extension` renders LaTeX during every reactive `marked.parse()`
 * call.  In streaming mode Vue's virtual-DOM diffing removes and re-adds DOM nodes on
 * every chunk, which conflicts with KaTeX's direct DOM manipulation and causes formulas
 * to flash and disappear.
 *
 * Solution: Before calling `marked.parse()` we extract every math block, render it to
 * static HTML with `katex.renderToString()` (no DOM involvement), and stash the result
 * behind a unique placeholder string.  After `marked.parse()` we substitute the
 * placeholders back.  The final HTML therefore contains self-contained KaTeX spans that
 * Vue's diffing can safely leave alone.
 */

import katex from 'katex';

export interface ExtractedFormula {
  placeholder: string;
  html: string;
}

/**
 * Pre-render all math in `rawText` and return a tuple of
 * [processedText, formulaMap] where `processedText` has math blocks
 * replaced by unique placeholder tokens and `formulaMap` maps each
 * token to its pre-rendered KaTeX HTML.
 */
export function extractAndPreRenderMath(rawText: string): [string, Map<string, string>] {
  const formulaMap = new Map<string, string>();
  let counter = 0;
  let processed = rawText;

  const renderFormula = (latex: string, displayMode: boolean): string => {
    const key = `\x00KATEX${counter++}\x00`;
    try {
      formulaMap.set(
        key,
        katex.renderToString(latex, {
          throwOnError: false,
          displayMode,
          output: 'html',
        }),
      );
    } catch {
      // Fall back to raw LaTeX wrapped in a code span so it is still legible.
      formulaMap.set(key, `<code>${latex}</code>`);
    }
    return key;
  };

  // Display math: $$…$$ (must come before inline $ matching)
  processed = processed.replace(/\$\$([\s\S]*?)\$\$/g, (_, latex) =>
    renderFormula(latex, true),
  );

  // Display math: \[…\]
  processed = processed.replace(/\\\[([\s\S]*?)\\\]/g, (_, latex) =>
    renderFormula(latex, true),
  );

  // Inline math: $…$ (single dollar, non-greedy, exclude already-processed)
  processed = processed.replace(/\$([^$\n]+?)\$/g, (match, latex) => {
    // Skip if content looks like a price (digit after $) — heuristic
    if (/^\d/.test(latex)) return match;
    return renderFormula(latex, false);
  });

  // Inline math: \(…\)
  processed = processed.replace(/\\\(([\s\S]*?)\\\)/g, (_, latex) =>
    renderFormula(latex, false),
  );

  return [processed, formulaMap];
}

/**
 * Restore pre-rendered KaTeX HTML by substituting placeholders back.
 */
export function restoreMathPlaceholders(html: string, formulaMap: Map<string, string>): string {
  let result = html;
  for (const [key, katexHtml] of formulaMap) {
    // The placeholder may be inside an HTML-encoded context — also try escaped version.
    result = result.split(key).join(katexHtml);
  }
  return result;
}

/**
 * Convenience wrapper: pre-render all math in `rawText`, run the
 * provided `markdownParser` on the result, then restore KaTeX HTML.
 *
 * @param rawText      Raw markdown string potentially containing LaTeX.
 * @param markdownParser  Function that converts (possibly math-free) markdown to HTML.
 */
export function renderMarkdownWithPrerenderedKatex(
  rawText: string,
  markdownParser: (md: string) => string,
): string {
  const [processedText, formulaMap] = extractAndPreRenderMath(rawText);
  const renderedHtml = markdownParser(processedText);
  return restoreMathPlaceholders(renderedHtml, formulaMap);
}
