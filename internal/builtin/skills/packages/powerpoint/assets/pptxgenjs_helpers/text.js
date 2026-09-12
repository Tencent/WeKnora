// Copyright (c) OpenAI. All rights reserved.
"use strict";

const { spawnSync } = require("child_process");
const { Canvas } = require("skia-canvas");
// Unicode line-break iterator (UAX #14) so we mimic PPT/LibreOffice wrapping rules.
const LineBreaker = require("linebreak");
const fontkit = require("fontkit");
const TEXT_MEASURER = getTextMeasurer();
const registeredFontVariants = new Set();
const fontPathCache = new Map();
const fontKitCache = new Map();

// Estimate the text box height for a given font size and line count.
// NOTE: This is an analytical approximation, not an exact reproduction of
// PowerPoint/LibreOffice layout. Always verify visually and adjust based on
// actual rendering if precise fit is required.
function calcTextBoxHeightSimple(
  fontSize,
  lines = 1,
  leading = 1.15,
  padding = 0.3
) {
  const lineHeightIn = (fontSize / 72) * leading;
  return lines * lineHeightIn + padding;
}

// Compute font size that fits given text within a fixed box.
// NOTE: autoFontSize uses skia-canvas measurement stack to approximate the font size
// that will fit in a given box. Rendering engines may differ slightly, so
// treat the result as an estimate and tweak as needed after visual inspection.
// Signature:
//   autoFontSize(textOrRuns, fontFace, opts?)
//   - fontFace must be provided as the 2nd positional argument and cannot be in opts.
//   - All modes always respect [minFontSize, maxFontSize] as a CLOSED interval when provided.
// Modes:
//   - mode: "shrink"  => shrink only (search [minFontSize, min(maxFontSize, fontSize)])
//   - mode: "enlarge" => enlarge only (search [max(minFontSize, fontSize), maxFontSize])
//   - mode: "auto"    => shrink + enlarge (search [minFontSize, maxFontSize]); fontSize optional.
// In "auto" mode fontSize is not required; when omitted we simply search the whole [minFontSize, maxFontSize] range.
// Returns a cloned options object with computed fontSize. fit: "shrink" is appended only when mode === "shrink".
function autoFontSize(textOrRuns, fontFace, opts = {}) {
  const x = toNumber(opts.x, 0);
  const y = toNumber(opts.y, 0);
  const w = toNumber(opts.w, 0);
  const h = toNumber(opts.h, 0);
  if (!(w > 0 && h > 0)) throw new Error("autoFontSize(): non-positive w or h");

  const face = typeof fontFace === "string" ? fontFace.trim() : "";
  if (face.length === 0) {
    throw new Error(
      "autoFontSize(): fontFace is required as the 2nd positional argument."
    );
  }

  // Fast-path: if there is no visible text content, just return the
  // (optionally clamped) reference fontSize; there is nothing to fit.
  const hasAnyText =
    normalizeText(textOrRuns).trim().length > 0 ||
    (Array.isArray(textOrRuns) &&
      textOrRuns.some(
        (run) => run && typeof run.text === "string" && run.text.trim().length
      ));

  const fontStyle =
    opts.italic === true || opts.fontStyle === "italic" ? "italic" : "normal";
  const fontWeight =
    opts.bold === true || String(opts.fontWeight || "").toLowerCase() === "bold"
      ? "bold"
      : "normal";
  const leading = toNumber(opts.leading, 1.15) || 1.15;

  const modeRaw = typeof opts.mode === "string" ? opts.mode : "auto"; // 'auto' (default) | 'shrink' | 'enlarge'
  const mode = modeRaw.toLowerCase();
  const isShrink = mode === "shrink";
  const isEnlarge = mode === "enlarge";
  const isAuto = mode === "auto";

  const refPtRaw = toNumber(opts.fontSize, NaN);
  const hasRefPt = Number.isFinite(refPtRaw);
  const refPt = hasRefPt ? refPtRaw : NaN;

  // Base bounds (closed interval). Defaults:
  //   - minFontSize: 1pt
  //   - maxFontSize: 1000pt (unless the caller provided a tighter bound)
  let minPt = toNumber(opts.minFontSize, NaN);
  let maxPt = toNumber(opts.maxFontSize, NaN);
  const userProvidedMax = Number.isFinite(maxPt);
  if (!Number.isFinite(minPt)) {
    minPt = 1;
  }
  if (!Number.isFinite(maxPt)) {
    maxPt = 1000;
  }

  if (isShrink || isEnlarge) {
    if (!hasRefPt) {
      throw new Error(
        "autoFontSize(): mode 'shrink' or 'enlarge' requires fontSize"
      );
    }
  }

  if (isShrink) {
    // Shrink only: never exceed the requested size (and respect maxFontSize).
    maxPt = Math.min(maxPt, refPt);
  } else if (isEnlarge) {
    // Enlarge only: never go below the requested size (and respect minFontSize).
    minPt = Math.max(minPt, refPt);
  } else if (isAuto && hasRefPt && userProvidedMax) {
    // Auto mode with an explicit maxFontSize: honor [minFontSize, maxFontSize]
    // as the search band while allowing both shrink and enlarge within it.
  } else if (!isAuto) {
    throw new Error(
      `autoFontSize(): unsupported mode "${modeRaw}", expected "auto" | "shrink" | "enlarge"`
    );
  }

  if (!(maxPt > 0 && maxPt >= minPt)) {
    throw new Error(
      "autoFontSize(): invalid minFontSize/maxFontSize bounds after normalization"
    );
  }

  // If there is no actual text, we can skip measurement entirely and just
  // clamp the reference size to [minPt, maxPt].
  if (!hasAnyText) {
    const chosen =
      (hasRefPt && Math.max(minPt, Math.min(maxPt, refPt))) || minPt;
    const out = { ...opts, x, y, w, h, fontSize: chosen };
    if (isShrink) out.fit = "shrink";
    return out;
  }

  // Search the space of candidate font sizes with a small step and a safety
  // bias baked into the fit test:
  //   - precision: 0.05pt (~1/20pt) so we land very close to the true max-fit.
  //   - safetyFactor: we require that the calcTextBox()-measured height is
  //     within a small margin of the caller-provided box height, so that the
  //     same layout engine used by calcTextBox drives autoFontSize decisions.
  const precision = 0.05; // point precision for search (~1/20pt)
  const safetyFactor = 0.97;

  let lo = minPt;
  let hi = maxPt;
  let best = lo;
  while (hi - lo > precision) {
    const mid = (lo + hi) / 2;
    // Delegate measurement to calcTextBox so that autoFontSize and
    // calcTextBox share the exact same layout pipeline (paragraph modeling,
    // bullet handling, margins, padding, width scaling, etc.).
    const layout = calcTextBox(mid, {
      text: textOrRuns,
      w,
      fontFace: face,
      fontStyle,
      fontWeight,
      leading,
      margin: opts.margin,
      padding: opts.padding,
      paraSpaceAfter: opts.paraSpaceAfter,
    });
    const fits = layout.h <= h * safetyFactor + 1e-6;
    if (fits) {
      best = mid;
      lo = mid; // try larger
    } else {
      hi = mid; // shrink
    }
  }
  // Closed interval: clamp to [minPt, maxPt].
  const finalPt = Math.max(minPt, Math.min(maxPt, best));

  // Pass through all original options, override fontSize and append fit: "shrink"
  const out = { ...opts, x, y, w, h, fontSize: finalPt };
  if (isShrink) out.fit = "shrink";
  return out;
}

// Calculate text box metrics using skia-canvas measurement (lines, height,
// width) for a given font size and text payload.
// NOTE: calcTextBox approximates how many lines and how much space text will
// occupy using our JS measurement pipeline. It is designed to be close to
// PowerPoint/LibreOffice but is not guaranteed pixel-perfect—always adjust
// based on actual slide rendering when precision matters.
// Signature:
//   calcTextBox(fontSizePt, opts)
//     - fontSizePt: number (points)
//     - opts (keywords): {
//         text?: string | runs[],
//         w?: number (inches),
//         h?: number (inches),
//         lines?: number,
//         fontFace?: string, // required when measuring by width/height with text
//         fontStyle?: 'normal' | 'italic', italic?: boolean,
//         fontWeight?: 'normal' | 'bold', bold?: boolean,
//         leading?: number (line height multiplier, default 1.15),
//         padding?: number (inches, default 0.3),
//         paraSpaceAfter?: number (points, default 0)
//       }
// Modes (auto-detected):
//   a) Given lines -> compute height
//   b) Given width + text -> compute height and lines
//   c) Given height + text -> compute width and lines
// Throws when insufficient info is provided.
function calcTextBox(fontSizePt, opts = {}) {
  const textInput = opts.text ?? "";
  const text = normalizeText(textInput || "");
  const face =
    typeof opts.fontFace === "string" && opts.fontFace.trim().length > 0
      ? opts.fontFace.trim()
      : "";
  const fontStyle =
    opts.italic === true || opts.fontStyle === "italic" ? "italic" : "normal";
  const fontWeight =
    opts.bold === true || String(opts.fontWeight || "").toLowerCase() === "bold"
      ? "bold"
      : "normal";
  const leading = toNumber(opts.leading, 1.15) || 1.15;
  const padding = toNumber(opts.padding, 0.3); // inches (allow 0)
  const paraSpaceAfterPt = toNumber(opts.paraSpaceAfter, 0) || 0; // points
  const lineHeightIn = (fontSizePt / 72) * leading;
  const margins = normalizeMargins(opts.margin);
  const measurer = TEXT_MEASURER;

  const hasLines = Number.isFinite(toNumber(opts.lines, NaN));
  const hasWidth = Number.isFinite(toNumber(opts.w, NaN));
  const hasHeight = Number.isFinite(toNumber(opts.h, NaN));
  const paragraphs = buildParagraphModels(textInput, {
    fontSizePt,
    // Do not silently substitute a default font here; callers measuring by
    // width/height are required to pass an explicit fontFace so that our
    // metrics match the actual slide theme.
    fontFace: face,
    fontStyle,
    fontWeight,
    leading,
    paraSpaceAfterPt,
  });
  const hasAnyText = paragraphs.some((p) => p.text.length > 0);

  // Empirical top inset: PPT text frames render a small gutter above the first line
  // even with zero margins. Model it as a fraction of the font size so callers can
  // visually trim by shifting y up and growing h by the same amount.
  const topInsetIn = (fontSizePt / 72) * 0.2; // ~20% of font size (inches)

  if (hasLines) {
    // Mode (a): Given lines -> compute height only
    const lines = toNumber(opts.lines, 1);
    const contentH = Math.max(0, lines * lineHeightIn + padding);
    const h = contentH + margins.top + margins.bottom;
    const passthrough = buildPassthroughOptions(opts, fontSizePt, margins);
    return {
      ...passthrough,
      w: toNumber(opts.w, NaN) || null,
      h,
      lines,
      contentH,
      margins,
      topInset: topInsetIn,
    };
  }

  if (hasWidth && hasAnyText) {
    // Mode (b): Given width + text -> compute height and lines
    if (face.length === 0) {
      throw new Error(
        "calcTextBox(): opts.fontFace is required when measuring by width."
      );
    }
    const boxW = toNumber(opts.w, 0);
    if (!(boxW > 0))
      throw new Error("calcTextBox(): width must be > 0 in mode 'width'");
    const innerW = Math.max(0, boxW - margins.left - margins.right);
    const { lines, heightIn } = layoutGivenWidth(paragraphs, innerW);
    const contentH = Math.max(0, heightIn + padding);
    const h = contentH + margins.top + margins.bottom;
    const passthrough = buildPassthroughOptions(opts, fontSizePt, margins);
    return {
      ...passthrough,
      w: boxW,
      h,
      lines,
      contentH,
      margins,
      topInset: topInsetIn,
    };
  }

  if (hasHeight && hasAnyText) {
    // Mode (c): Given height + text -> compute minimal width and lines to fit
    if (face.length === 0) {
      throw new Error(
        "calcTextBox(): opts.fontFace is required when measuring by height."
      );
    }
    const boxH = toNumber(opts.h, 0);
    if (!(boxH > 0))
      throw new Error("calcTextBox(): height must be > 0 in mode 'height'");
    const innerH = Math.max(0, boxH - margins.top - margins.bottom);
    // Upper bound: single-line width across paragraphs
    const singleLineWidth = paragraphs.reduce((mx, p) => {
      const width = measureRunWidth(p, p.text) + p.textIndentIn;
      return Math.max(mx, width);
    }, 0);
    const minHeightOneLine = Math.max(
      0,
      paragraphs.reduce((sum, p, idx) => {
        const lineHeight = (p.fontSizePt / 72) * p.leading;
        sum += lineHeight;
        if (idx !== paragraphs.length - 1) sum += p.paraSpaceAfterIn;
        return sum;
      }, 0)
    );
    if (minHeightOneLine + padding - innerH > 1e-6) {
      throw new Error(
        "calcTextBox(): height too small for one-line layout at this font size"
      );
    }
    // Lower bound: longest token width
    const longestTokenWidth = paragraphs.reduce((mx, p) => {
      const tokens = splitTextIntoTokens(p.text);
      for (const tk of tokens) {
        if (tk.length === 0) continue;
        const wIn = measureRunWidth(p, tk) + p.textIndentIn;
        if (wIn > mx) mx = wIn;
      }
      return mx;
    }, 0);
    let lo = Math.max(0.01, longestTokenWidth);
    let hi = Math.max(lo, singleLineWidth);
    let best = hi;
    for (let iter = 0; iter < 32; iter++) {
      const mid = (lo + hi) / 2;
      const { lines, heightIn } = layoutGivenWidth(paragraphs, mid);
      const totalH = heightIn + padding;
      if (totalH <= innerH + 1e-6) {
        best = mid;
        hi = mid;
      } else {
        lo = mid;
      }
    }
    const { lines, heightIn } = layoutGivenWidth(paragraphs, best);
    const contentH = heightIn + padding;
    const passthrough = buildPassthroughOptions(opts, fontSizePt, margins);
    return {
      ...passthrough,
      w: best + margins.left + margins.right,
      h: contentH + margins.top + margins.bottom,
      lines,
      contentH,
      margins,
      topInset: topInsetIn,
    };
  }

  throw new Error(
    "calcTextBox(): insufficient information. Provide {lines} or ({w,text}) or ({h,text})."
  );
}

function layoutGivenWidth(paragraphs, boxW) {
  let totalLines = 0;
  let heightIn = 0;
  for (let i = 0; i < paragraphs.length; i++) {
    const para = paragraphs[i];
    const widthScale = getWidthScaleForParagraph(para);
    const usableWidth = Math.max(0.01, boxW - para.textIndentIn) * widthScale;
    const lines = greedyWrap(para, usableWidth);
    const count = Math.max(1, lines.length);
    totalLines += count;
    const lineHeightIn = (para.fontSizePt / 72) * para.leading;
    heightIn += count * lineHeightIn;
    if (i !== paragraphs.length - 1) heightIn += para.paraSpaceAfterIn;
  }
  return { lines: totalLines, heightIn };
}

function greedyWrap(paragraph, maxWidthIn) {
  const text = paragraph.text || "";
  if (text.length === 0) return [""];
  const breaker = new LineBreaker(text);
  const breakpoints = [];
  let bk;
  while ((bk = breaker.nextBreak())) {
    breakpoints.push({ pos: bk.position, required: bk.required });
  }
  const lines = [];
  let start = skipTextWhitespace(text, 0);
  let idx = 0;
  while (start < text.length) {
    while (idx < breakpoints.length && breakpoints[idx].pos <= start) idx++;
    let chosen = null;
    let probe = idx;
    while (probe < breakpoints.length) {
      const br = breakpoints[probe];
      const slice = text.slice(start, br.pos);
      const width = measureRunWidth(paragraph, trimLineEnd(slice));
      if (width <= maxWidthIn + 1e-6) {
        chosen = br;
        probe++;
        if (br.required) break;
      } else {
        break;
      }
    }
    if (!chosen) {
      const forced = forceBreakSegment(text, start, maxWidthIn, paragraph);
      if (forced.segment.length === 0) break;
      lines.push(trimLineEnd(forced.segment));
      start = skipTextWhitespace(text, forced.nextIndex);
      continue;
    }
    const lineText = text.slice(start, chosen.pos);
    lines.push(trimLineEnd(lineText));
    start = skipTextWhitespace(text, chosen.pos);
  }
  if (!lines.length) lines.push("");
  return lines;
}

function splitTextIntoTokens(text) {
  if (typeof text !== "string") return [""];
  const tokens = text.split(/(\s+)/);
  return tokens.length ? tokens : [""];
}

function trimLineEnd(value) {
  return typeof value === "string" ? value.replace(/\s+$/u, "") : "";
}

function measureRunWidth(paragraph, text) {
  if (!text || text.length === 0) return 0;
  const fontData = getFontData(
    paragraph.fontFace,
    paragraph.fontStyle,
    paragraph.fontWeight
  );
  if (fontData && fontData.font) {
    const layout = fontData.font.layout(text);
    const widthPts =
      (layout.advanceWidth / fontData.font.unitsPerEm) * paragraph.fontSizePt;
    return Math.max(0, widthPts / 72);
  }
  return TEXT_MEASURER(
    text,
    paragraph.fontSizePt,
    paragraph.fontFace,
    paragraph.fontStyle,
    paragraph.fontWeight
  );
}

function forceBreakSegment(text, start, maxWidthIn, paragraph) {
  const chars = Array.from(text.slice(start));
  if (chars.length === 0) return { segment: "", nextIndex: text.length };
  let buffer = "";
  let consumedUnits = 0;
  for (let i = 0; i < chars.length; i++) {
    const candidate = buffer + chars[i];
    const width = measureRunWidth(paragraph, trimLineEnd(candidate));
    if (width <= maxWidthIn + 1e-6) {
      buffer = candidate;
      consumedUnits += chars[i].length;
      continue;
    }
    if (buffer.length === 0) {
      buffer = chars[i];
      consumedUnits += chars[i].length;
    }
    break;
  }
  if (buffer.length === 0) {
    buffer = chars[0] || "";
    consumedUnits = buffer.length;
  }
  return { segment: buffer, nextIndex: start + consumedUnits };
}

function skipTextWhitespace(text, index) {
  let idx = index;
  while (idx < text.length && /\s/.test(text[idx])) idx++;
  return idx;
}

function buildParagraphModels(textOrRuns, baseStyle) {
  const entries = collectParagraphEntries(textOrRuns);
  if (entries.length === 0) {
    return [resolveParagraphStyle({ text: "" }, baseStyle)];
  }
  return entries.map((entry) => resolveParagraphStyle(entry, baseStyle));
}

function collectParagraphEntries(textOrRuns) {
  const result = [];
  if (Array.isArray(textOrRuns)) {
    for (const entry of textOrRuns) {
      if (typeof entry === "string") {
        pushParagraphSegments(entry, undefined, result);
      } else if (entry && typeof entry === "object") {
        pushParagraphSegments(entry.text ?? "", entry.options || {}, result);
      }
    }
    return result;
  }
  pushParagraphSegments(textOrRuns ?? "", undefined, result);
  return result;
}

function pushParagraphSegments(text, options, target) {
  const normalized = String(text ?? "");
  const parts = normalized.split(/\r?\n/);
  if (parts.length === 0) {
    target.push({ text: "", options });
    return;
  }
  for (const part of parts) {
    target.push({ text: part, options });
  }
}

function resolveParagraphStyle(entry, baseStyle) {
  const opts = entry.options || {};
  const fontFace =
    (opts.fontFace && String(opts.fontFace).trim()) ||
    baseStyle.fontFace ||
    "Arial";
  const fontStyle =
    opts.italic === true || opts.fontStyle === "italic"
      ? "italic"
      : baseStyle.fontStyle || "normal";
  const fontWeight =
    opts.bold === true || String(opts.fontWeight || "").toLowerCase() === "bold"
      ? "bold"
      : baseStyle.fontWeight || "normal";
  const fontSizePt =
    toNumber(opts.fontSize, baseStyle.fontSizePt) || baseStyle.fontSizePt;
  const leading =
    toNumber(opts.leading, baseStyle.leading) || baseStyle.leading || 1.15;
  const paraSpaceAfterPt =
    toNumber(opts.paraSpaceAfter, baseStyle.paraSpaceAfterPt) ||
    baseStyle.paraSpaceAfterPt ||
    0;
  const hasBullet = !!opts.bullet;
  let indentPt = toNumber(opts.indent, NaN);
  if (!Number.isFinite(indentPt) && hasBullet) {
    indentPt = toNumber(opts.bullet.indent, NaN);
  }
  if (!Number.isFinite(indentPt)) indentPt = 0;
  const hangingPt = toNumber(opts.hanging, 0) || 0;
  let textIndentIn = 0;
  if (indentPt > 0) {
    if (hasBullet) {
      // PowerPoint-style bullets: "indent" is the distance from the left edge
      // of the text box to the start of the text (the bullet itself is hung
      // using the hanging value). This means the available width for the text
      // is boxWidth - indent, not boxWidth - (indent - hanging). Modeling it
      // this way matches the manual line counts from PowerPoint/LibreOffice.
      textIndentIn = indentPt / 72;
    } else {
      // Non-bullet paragraphs keep the prior behavior where hanging reduces
      // the effective indent (similar to CSS text-indent).
      textIndentIn = Math.max(0, (indentPt - hangingPt) / 72);
    }
  }
  return {
    text: entry.text || "",
    fontFace,
    fontStyle,
    fontWeight,
    fontSizePt,
    leading,
    paraSpaceAfterIn: paraSpaceAfterPt / 72,
    textIndentIn,
  };
}

function getFontData(face, fontStyle, fontWeight) {
  const key = makeFontCacheKey(face, fontStyle, fontWeight);
  if (fontKitCache.has(key)) return fontKitCache.get(key);
  const fontPath = findFontPath(face, fontStyle, fontWeight);
  if (!fontPath) {
    fontKitCache.set(key, null);
    return null;
  }
  try {
    let font = fontkit.openSync(fontPath);
    if (font && typeof font.fonts === "object") {
      font = selectCollectionFont(font, fontStyle, fontWeight);
    }
    if (!font || typeof font.layout !== "function") {
      fontKitCache.set(key, null);
      return null;
    }
    registerCanvasFontVariant(fontPath, face, fontStyle, fontWeight, key);
    const payload = { font, path: fontPath };
    fontKitCache.set(key, payload);
    return payload;
  } catch (err) {
    fontKitCache.set(key, null);
    return null;
  }
}

function makeFontCacheKey(face, fontStyle, fontWeight) {
  const family = (face || "Arial").trim();
  const style = (fontStyle || "normal").toLowerCase();
  const weight = (fontWeight || "normal").toLowerCase();
  return `${family}::${style}::${weight}`;
}

function registerCanvasFontVariant(
  fontPath,
  face,
  fontStyle,
  fontWeight,
  cacheKey
) {
  if (registeredFontVariants.has(cacheKey)) return;
  try {
    Canvas.registerFont(fontPath, {
      family: face,
      style: fontStyle || "normal",
      weight: fontWeight || "normal",
    });
    registeredFontVariants.add(cacheKey);
  } catch (err) {
    // ignore registration failure; measurement will fall back to Skia default
  }
}

function findFontPath(face, fontStyle, fontWeight) {
  const family = (face || "").trim();
  if (family.length === 0) return null;
  const key = makeFontCacheKey(family, fontStyle, fontWeight);
  if (fontPathCache.has(key)) return fontPathCache.get(key);
  const styleParts = [];
  if ((fontWeight || "").toLowerCase() === "bold") styleParts.push("Bold");
  if ((fontStyle || "").toLowerCase() === "italic") styleParts.push("Italic");
  const styleQuery =
    styleParts.length > 0 ? `:style=${styleParts.join(" ")}` : "";
  const query = `${family}${styleQuery}`;
  const result = spawnSync("fc-match", ["-f", "%{file}", query], {
    encoding: "utf8",
  });
  if (result.status === 0) {
    const output = String(result.stdout || "").trim();
    if (output.length > 0) {
      fontPathCache.set(key, output);
      return output;
    }
  }
  fontPathCache.set(key, null);
  return null;
}

function selectCollectionFont(collection, fontStyle, fontWeight) {
  const fonts = collection.fonts || [];
  if (fonts.length === 0) return null;
  const wantItalic = (fontStyle || "").toLowerCase() === "italic";
  const wantBold = (fontWeight || "").toLowerCase() === "bold";
  let best = fonts[0];
  let bestScore = scoreFontVariant(best, wantItalic, wantBold);
  for (let i = 1; i < fonts.length; i++) {
    const candidate = fonts[i];
    const score = scoreFontVariant(candidate, wantItalic, wantBold);
    if (score > bestScore) {
      best = candidate;
      bestScore = score;
    }
  }
  return best;
}

function scoreFontVariant(font, wantItalic, wantBold) {
  if (!font) return -1;
  const name = String(font.fullName || font.postscriptName || "").toLowerCase();
  const isItalic = /italic|oblique/.test(name);
  const isBold = /bold|black|heavy|semibold|extrabold/.test(name);
  let score = 0;
  if (isItalic === wantItalic) score += 1;
  if (isBold === wantBold) score += 1;
  return score;
}

// Empirical width scaling to better match PowerPoint/LibreOffice line breaks.
// A tiny global shrink (about -1.5%) nudges borderline words to wrap the same
// way Office does, with per-script tweaks for cases where our measurer
// systematically under- or over-estimates glyph widths. We intentionally avoid
// per-font calibration so this helper generalizes beyond the regression deck.
function getWidthScaleForParagraph(paragraph) {
  if (!paragraph || typeof paragraph.text !== "string") return 1;
  const text = paragraph.text;
  // Thai script: our measurer tends to slightly over-estimate, which can cause
  // extra wraps. Give it a bit more room horizontally.
  if (/[ก-๛]/u.test(text)) {
    return 1.2;
  }

  // Arabic: we usually underestimate, so shrink available width a bit more to
  // encourage earlier breaks.
  if (/[\u0600-\u06FF]/u.test(text)) {
    return 0.97;
  }

  // Base shrink for most Latin and other scripts.
  return 0.985;
}

// Build options to pass directly to pptx.addText. We exclude measurement-only
// fields and fill sensible defaults (e.g., fontSize) so callers can spread
// the result into addText just like the image sizing helpers.
function buildPassthroughOptions(opts, fontSizePt, margins) {
  const exclude = new Set([
    "text",
    "lines",
    "w", // will be set by calcTextBox
    "h", // will be set by calcTextBox
    // fontFace/style/weight are useful for addText; allow passthrough
    "leading",
    "padding",
  ]);
  const out = {};
  for (const k of Object.keys(opts)) {
    if (!exclude.has(k)) out[k] = opts[k];
  }
  if (out.fontSize == null) out.fontSize = fontSizePt;
  if (opts.margin != null) out.margin = margins;
  return out;
}

function getTextMeasurer() {
  // Skia-canvas only for accurate shaping and Fontconfig-based resolution.
  // Throws if skia-canvas is not available.
  const canvas = new Canvas(2, 2);
  const ctx = canvas.getContext("2d");
  const PX_PER_IN = 96;
  return (text, fontSizePt, fontFace, fontStyle, fontWeight) => {
    const px = (fontSizePt / 72) * PX_PER_IN;
    const style = fontStyle || "normal";
    const weight = fontWeight || "normal";
    // CSS shorthand: style weight size family
    ctx.font = `${style} ${weight} ${px}px ${fontFace || "Arial"}`;
    const metrics = ctx.measureText(text);
    return (metrics.width || 0) / PX_PER_IN;
  };
}

function normalizeMargins(m) {
  const toInches = (value) =>
    typeof value === "number" && Number.isFinite(value) ? value / 72 : 0;
  if (m && typeof m === "object") {
    if (Number.isFinite(m.left) || Number.isFinite(m.top)) {
      return {
        left: toInches(m.left),
        right: toInches(m.right),
        top: toInches(m.top),
        bottom: toInches(m.bottom),
      };
    }
  }
  const all = toInches(m);
  return { left: all, right: all, top: all, bottom: all };
}

function normalizeText(textOrRuns) {
  if (Array.isArray(textOrRuns)) {
    return textOrRuns
      .map((item) => {
        if (typeof item === "string") return item;
        if (item && typeof item.text === "string") return item.text;
        return "";
      })
      .join("");
  }
  return typeof textOrRuns === "string" ? textOrRuns : String(textOrRuns ?? "");
}

function toNumber(v, fallback) {
  const n = typeof v === "string" ? parseFloat(v) : v;
  return Number.isFinite(n) ? n : fallback;
}

module.exports = {
  calcTextBoxHeightSimple,
  calcTextBox,
  autoFontSize,
};
