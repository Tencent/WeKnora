import { Marked, type MarkedExtension } from 'marked'
import markedKatex from 'marked-katex-extension'
import { literalSingleTildeExtension } from './markedLiteralTilde.ts'

// Shared by the worker lexer and the HTML renderer. This module has no DOM dependencies.
export function createPreviewMarkdownParser(options: MarkedExtension = {}): Marked {
  const parser = new Marked({ breaks: true, gfm: true, ...options })
  parser.use(markedKatex({ throwOnError: false, nonStandard: true }))
  parser.use(literalSingleTildeExtension)
  return parser
}

export function preprocessMathDelimiters(rawText: string): string {
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$')
}
