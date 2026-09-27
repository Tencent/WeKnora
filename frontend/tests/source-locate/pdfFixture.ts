/** Tiny real PDF fixture, generated without a PDF library or network service. */
export function fixturePdf(pages: Array<{ lines: Array<[string, number, number]>; rotation?: number }>): ArrayBuffer {
  const objects: string[] = []
  objects[0] = '<< /Type /Catalog /Pages 2 0 R >>'
  objects[1] = `<< /Type /Pages /Count ${pages.length} /Kids [${pages.map((_, i) => `${4 + i * 2} 0 R`).join(' ')}] >>`
  objects[2] = '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'
  pages.forEach((page, i) => {
    const stream = page.lines.map(([text, x, y]) => `BT /F1 14 Tf ${x} ${y} Td (${text.replace(/[\\()]/g, '\\$&')}) Tj ET`).join('\n')
    objects[3 + i * 2] = `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Rotate ${page.rotation || 0} /Resources << /Font << /F1 3 0 R >> >> /Contents ${5 + i * 2} 0 R >>`
    objects[4 + i * 2] = `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`
  })
  let pdf = '%PDF-1.4\n'
  const offsets = [0]
  objects.forEach((object, i) => { offsets.push(pdf.length); pdf += `${i + 1} 0 obj\n${object}\nendobj\n` })
  const xref = pdf.length
  pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  pdf += offsets.slice(1).map((offset) => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')
  pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`
  return new TextEncoder().encode(pdf).buffer
}
