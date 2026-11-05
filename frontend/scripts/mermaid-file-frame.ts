import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import { build, type Plugin } from 'vite'

const moduleId = 'virtual:mermaid-file-frame'
const source = fileURLToPath(new URL('../src', import.meta.url))

export function frameDocument(code: string): string {
  const script = code.replace(/<\/script/gi, '<\\/script')
  const hash = createHash('sha256').update(script).digest('base64')
  const policy = `default-src 'none'; script-src 'sha256-${hash}'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'`
  return `<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${policy}"><meta name="referrer" content="no-referrer"><meta http-equiv="x-dns-prefetch-control" content="off"><style>body{margin:0;font-family:sans-serif}#diagram{display:grid;place-items:center;min-height:100vh}#diagram>svg{max-width:100%;height:auto}</style><main id="diagram" inert></main><script>${script}</script>`
}

// Bundle the complete renderer into a lazy application module. The opaque-origin
// iframe can then parse Mermaid without fetching scripts, styles or diagram assets.
export function mermaidFileFrame(): Plugin {
  return {
    name: 'mermaid-file-frame',
    resolveId(id) {
      if (id === moduleId) return '\0' + moduleId
    },
    async load(id) {
      if (id !== '\0' + moduleId) return
      const result = await build({
        configFile: false,
        publicDir: false,
        root: fileURLToPath(new URL('..', import.meta.url)),
        logLevel: 'warn',
        resolve: {
          alias: { '@/i18n': source + '/components/mermaid-file/labels.ts', '@': source },
        },
        build: {
          write: false,
          lib: {
            entry: source + '/components/mermaid-file/frame.ts',
            name: 'mermaidFile',
            formats: ['iife'],
          },
          rollupOptions: { output: { inlineDynamicImports: true } },
        },
      })
      const outputs = Array.isArray(result) ? result : [result]
      const chunk = outputs
        .flatMap((output) => ('output' in output ? output.output : []))
        .find((item) => item.type === 'chunk' && item.isEntry)
      if (!chunk || chunk.type !== 'chunk') throw new Error('Missing Mermaid file renderer')
      for (const file of chunk.moduleIds) {
        if (file.startsWith(source + '/')) this.addWatchFile(file)
      }
      return 'export default ' + JSON.stringify(frameDocument(chunk.code))
    },
  }
}
