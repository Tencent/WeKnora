import { renderMermaidToSvg } from '@/utils/mermaidShared'
import { downloadSvgAsImage, openMermaidFullscreen } from '@/utils/mermaidViewer'
import { labels } from './labels'
import { prepareDiagram } from './prepareDiagram'

const mount = document.querySelector<HTMLElement>('#diagram')!
let closeViewer: (() => void) | undefined
let diagram: SVGSVGElement | null = null
let connected = false

// No interactive document content is required for a file diagram. Leave controls
// outside the inert surface. Block links even during Mermaid's temporary DOM layout.
for (const event of ['click', 'auxclick', 'submit', 'dragstart']) {
  document.addEventListener(
    event,
    (e) => {
      if (e.target instanceof Element && e.target.closest('a,area,form')) {
        e.preventDefault()
        e.stopImmediatePropagation()
      }
    },
    true,
  )
}
window.addEventListener('message', (e) => {
  if (
    connected ||
    window.origin !== 'null' ||
    e.source !== parent ||
    e.origin !== new URL(document.baseURI).origin ||
    e.data !== 'mermaid:init' ||
    e.ports.length !== 1
  )
    return
  connected = true
  const port = e.ports[0]
  let started = false
  port.onmessage = async ({ data, ports }) => {
    if (data?.type === 'source' && !started) {
      started = true
      Object.assign(labels, data.labels)
      document.body.inert = true
      document.documentElement.setAttribute('theme-mode', data.dark ? 'dark' : 'light')
      try {
        const result = await renderMermaidToSvg(data.source)
        if (!result) throw Error('Mermaid did not produce SVG')
        const template = document.createElement('template')
        template.innerHTML = result
        diagram = template.content.querySelector('svg')
        if (!diagram) throw Error('Missing SVG')
        prepareDiagram(diagram)
        mount.replaceChildren(diagram)
        port.postMessage('ready')
      } catch {
        port.postMessage('failed')
      } finally {
        document.body.inert = false
      }
    } else if (data?.type === 'open' && diagram && !closeViewer) {
      closeViewer = openMermaidFullscreen(diagram, {
        close() {
          closeViewer = undefined
          port.postMessage('closed')
        },
        hostExports: true,
      })
    } else if (data?.type === 'export' && ports.length === 1) {
      const reply = ports[0]
      try {
        if (!diagram || !closeViewer) throw Error('Viewer closed')
        await downloadSvgAsImage(diagram, (blob) => reply.postMessage(blob))
      } catch {
        reply.postMessage(null)
      } finally {
        reply.close()
      }
    } else if (data?.type === 'close') closeViewer?.()
  }
  port.start()
})
