/**
 * Adobe Symbol byte encoding to Unicode (zero means no standard Unicode mapping).
 * Source: https://www.unicode.org/Public/MAPPINGS/VENDORS/ADOBE/symbol.txt
 * For duplicate mappings, use the first standard Unicode entry in the source.
 *
 * Copyright (c) 1991-2011 Unicode, Inc. All Rights reserved.
 *
 * This file is provided as-is by Unicode, Inc. (The Unicode Consortium). No
 * claims are made as to fitness for any particular purpose. No warranties of
 * any kind are expressed or implied. The recipient agrees to determine
 * applicability of information provided. If this file has been provided on
 * magnetic media by Unicode, Inc., the sole remedy for any claim will be
 * exchange of defective media within 90 days of receipt.
 *
 * Unicode, Inc. hereby grants the right to freely use the information
 * supplied in this file in the creation of products supporting the
 * Unicode Standard, and to make copies of this file in any form for
 * internal or external distribution as long as this notice remains
 * attached.
 */
const symbolCodePoints = [
  0x0020, 0x0021, 0x2200, 0x0023, 0x2203, 0x0025, 0x0026, 0x220b, 0x0028, 0x0029, 0x2217, 0x002b, 0x002c, 0x2212, 0x002e, 0x002f, // 20–2F
  0x0030, 0x0031, 0x0032, 0x0033, 0x0034, 0x0035, 0x0036, 0x0037, 0x0038, 0x0039, 0x003a, 0x003b, 0x003c, 0x003d, 0x003e, 0x003f, // 30–3F
  0x2245, 0x0391, 0x0392, 0x03a7, 0x0394, 0x0395, 0x03a6, 0x0393, 0x0397, 0x0399, 0x03d1, 0x039a, 0x039b, 0x039c, 0x039d, 0x039f, // 40–4F
  0x03a0, 0x0398, 0x03a1, 0x03a3, 0x03a4, 0x03a5, 0x03c2, 0x03a9, 0x039e, 0x03a8, 0x0396, 0x005b, 0x2234, 0x005d, 0x22a5, 0x005f, // 50–5F
  0x0000, 0x03b1, 0x03b2, 0x03c7, 0x03b4, 0x03b5, 0x03c6, 0x03b3, 0x03b7, 0x03b9, 0x03d5, 0x03ba, 0x03bb, 0x00b5, 0x03bd, 0x03bf, // 60–6F
  0x03c0, 0x03b8, 0x03c1, 0x03c3, 0x03c4, 0x03c5, 0x03d6, 0x03c9, 0x03be, 0x03c8, 0x03b6, 0x007b, 0x007c, 0x007d, 0x223c, 0x0000, // 70–7F
  0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, // 80–8F
  0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, // 90–9F
  0x20ac, 0x03d2, 0x2032, 0x2264, 0x2044, 0x221e, 0x0192, 0x2663, 0x2666, 0x2665, 0x2660, 0x2194, 0x2190, 0x2191, 0x2192, 0x2193, // A0–AF
  0x00b0, 0x00b1, 0x2033, 0x2265, 0x00d7, 0x221d, 0x2202, 0x2022, 0x00f7, 0x2260, 0x2261, 0x2248, 0x2026, 0x0000, 0x0000, 0x21b5, // B0–BF
  0x2135, 0x2111, 0x211c, 0x2118, 0x2297, 0x2295, 0x2205, 0x2229, 0x222a, 0x2283, 0x2287, 0x2284, 0x2282, 0x2286, 0x2208, 0x2209, // C0–CF
  0x2220, 0x2207, 0x0000, 0x0000, 0x0000, 0x220f, 0x221a, 0x22c5, 0x00ac, 0x2227, 0x2228, 0x21d4, 0x21d0, 0x21d1, 0x21d2, 0x21d3, // D0–DF
  0x25ca, 0x2329, 0x0000, 0x0000, 0x0000, 0x2211, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, // E0–EF
  0x0000, 0x232a, 0x222b, 0x2320, 0x0000, 0x2321, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, 0x0000, // F0–FF
];

// Only the part of docx-preview's parsed document used by this adapter.
// Keep this boundary local: the library does not publish a typed document model.
interface NumberingDocument {
  numberingPart?: {
    domNumberings: ReadonlyArray<{
      format?: string;
      levelText?: string;
      bullet?: unknown;
      rStyle?: Record<string, string>;
    }>;
  };
  fontTablePart?: {
    fonts: Array<{ name: string; embedFontRefs?: unknown[] }>;
  };
}

function isSymbolFont(font: string | undefined): boolean {
  return /^(?:Symbol|"Symbol"|'Symbol')$/i.test(font?.trim() ?? '');
}

/** Normalize legacy Symbol list markers in the in-memory preview only. */
export function normalizeDocxListSymbols(document: NumberingDocument): void {
  // An embedded font owns its glyph mapping and may intentionally use private codes.
  if (document.fontTablePart?.fonts.some(font =>
    isSymbolFont(font.name) && font.embedFontRefs?.length,
  )) return;

  for (const numbering of document.numberingPart?.domNumberings ?? []) {
    if (numbering.format !== 'bullet' || numbering.bullet ||
        !isSymbolFont(numbering.rStyle?.['font-family']) || !numbering.levelText) continue;

    let changed = false;
    let unsupported = false;
    const text = Array.from(numbering.levelText, character => {
      const code = character.codePointAt(0)!;
      if (code < 0xf000 || code > 0xf0ff) return character;
      const mapped = symbolCodePoints[code - 0xf020];
      if (!mapped) {
        unsupported = true;
        return character;
      }
      changed = true;
      return String.fromCodePoint(mapped);
    }).join('');

    // Keep a composite marker intact if any legacy glyph could not be translated.
    if (changed && !unsupported) {
      numbering.levelText = text;
    }
  }
}
