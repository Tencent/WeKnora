// Keep Mermaid's labels, CSS and internal marker IDs. The preview is inert and
// its sandbox CSP also applies while Mermaid is generating this SVG.
const activeElements = new Set([
  'script',
  'iframe',
  'object',
  'embed',
  'meta',
  'area',
  'link',
  'base',
  'form',
  'animate',
  'animatemotion',
  'animatetransform',
  'set',
])

export function prepareDiagram(svg: SVGSVGElement): void {
  for (const node of Array.from(svg.getElementsByTagName('*'))) {
    const parent = node.parentNode
    if (!parent) continue
    const tag = node.localName.toLowerCase()
    if (activeElements.has(tag)) {
      parent.removeChild(node)
    } else if (tag === 'a') {
      while (node.firstChild) parent.insertBefore(node.firstChild, node)
      parent.removeChild(node)
    }
  }
}
