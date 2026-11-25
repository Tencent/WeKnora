addEventListener('message', async function render(event) {
  if (
    window.origin !== 'null' ||
    event.source !== parent ||
    event.origin !== new URL(document.baseURI).origin ||
    event.ports.length !== 1
  )
    return
  removeEventListener('message', render)
  const port = event.ports[0]
  try {
    mermaid.initialize(event.data.config)
    const images = []
    for (const [index, source] of event.data.sources.entries()) {
      try {
        images.push(await renderImage(source, index))
      } catch (error) {
        console.error('Mermaid rendering failed', String(error))
        images.push(null)
      }
    }
    port.postMessage(images)
  } catch {
    port.postMessage(null)
  } finally {
    port.close()
  }
})

async function renderImage(source, index) {
  const { svg } = await mermaid.render(`file-diagram-${index}`, source)
  const tpl = document.createElement('template')
  tpl.innerHTML = svg
  const root = tpl.content.querySelector('svg')
  const [, , w, h] = root
    .getAttribute('viewBox')
    .split(/[,\s]+/)
    .map(Number)
  if (!(w > 0 && h > 0 && Number.isFinite(w) && Number.isFinite(h))) throw Error('Invalid size')
  root.setAttribute('width', Math.ceil(w))
  root.setAttribute('height', Math.ceil(h))
  return new XMLSerializer().serializeToString(root)
}
