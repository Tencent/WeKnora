import mermaidUrl from 'mermaid/dist/mermaid.min.js?url&no-inline'
import { mermaidConfig } from '@/utils/mermaidConfig'
import { frameDocument } from './frameDocument'
import bootstrap from './render.js?raw'

/** Render untrusted diagram text in an opaque-origin frame and use its SVG only as an image. */
export async function renderMermaidImages(
  sources: readonly string[],
  signal: AbortSignal,
): Promise<Array<string | null>> {
  signal.throwIfAborted()
  const controller = new AbortController()
  const abort = () => controller.abort(signal.reason)
  signal.addEventListener('abort', abort, { once: true })
  const timeout = setTimeout(() => controller.abort(), 15000)
  const frame = document.createElement('iframe')
  let channel: MessageChannel | undefined

  try {
    const response = await fetch(mermaidUrl, { signal: controller.signal })
    if (!response.ok) throw new Error('Mermaid unavailable')
    frame.srcdoc = frameDocument(await response.text(), bootstrap)
    frame.sandbox.add('allow-scripts')
    frame.referrerPolicy = 'no-referrer'
    frame.setAttribute('aria-hidden', 'true')
    frame.tabIndex = -1
    frame.style.cssText = 'position:fixed;inset:0;width:100%;height:100%;border:0;visibility:hidden'
    controller.signal.throwIfAborted()

    return await new Promise((resolve, reject) => {
      controller.signal.addEventListener('abort', () => reject(controller.signal.reason), { once: true })
      frame.addEventListener('load', () => {
        if (!frame.contentWindow) return reject(new Error('Mermaid frame unavailable'))
        channel = new MessageChannel()
        channel.port1.onmessage = ({ data }) => {
          if (
            !Array.isArray(data) ||
            data.length !== sources.length ||
            data.some(svg => svg !== null && typeof svg !== 'string')
          ) {
            reject(new Error('Invalid Mermaid frame response'))
            return
          }
          resolve(data.map(svg => svg ? 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg) : null))
        }
        const config = mermaidConfig(document.documentElement.getAttribute('theme-mode') === 'dark')
        frame.contentWindow.postMessage({ sources, config }, '*', [channel.port2])
      }, { once: true })
      document.body.append(frame)
    })
  } finally {
    clearTimeout(timeout)
    signal.removeEventListener('abort', abort)
    channel?.port1.close()
    frame.remove()
  }
}
