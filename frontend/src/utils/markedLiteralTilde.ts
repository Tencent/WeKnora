import { marked, type MarkedExtension } from 'marked'

// marked's GFM `del` tokenizer matches a lone `~` (e.g. the range
// `2020~2035`), turning it into strikethrough. Override it so a `~~`-prefixed
// run returns `false` and falls back to the built-in rule, while anything
// else returns `undefined` and renders the `~` as literal text.
export const literalSingleTildeExtension: MarkedExtension = {
  tokenizer: {
    del(src: string) {
      if (src.startsWith('~~')) return false
      return undefined
    },
  },
}

let registeredOnGlobalMarked = false

// Every `marked.use` call wraps the global tokenizer once more, so components
// that share the global `marked` must not register this per mount.
export function ensureLiteralSingleTildeOnGlobalMarked(): void {
  if (registeredOnGlobalMarked) return
  marked.use(literalSingleTildeExtension)
  registeredOnGlobalMarked = true
}
