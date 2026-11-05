/**
 * Preserve a well-formed Mermaid SVG in the browser's inert image mode.
 * Only a fresh SVG/image wrapper enters the active document or fullscreen viewer.
 * SVG image mode disables scripts, interactivity, and external resource loads.
 * https://developer.mozilla.org/en-US/docs/Web/SVG/Reference/Element/image
 */
export function mermaidImage(svg: string): string | null {
  const match = svg.match(/<svg\b[^>]*\bviewBox="([\d.eE+,\s-]+)"/i)
  const box = match?.[1]
    .trim()
    .split(/[\s,]+/)
    .map(Number)
  if (!box || box.length !== 4 || !box.every(Number.isFinite) || box[2] <= 0 || box[3] <= 0)
    return null
  const [x, y, width, height] = box
  const source = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg)
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${box.join(' ')}" width="${width}" height="${height}"><image x="${x}" y="${y}" width="${width}" height="${height}" href="${source}"/></svg>`
}
