import JSZip from 'jszip'
import * as XLSX from 'xlsx'

export async function docxFixture(): Promise<Blob> {
  const zip = new JSZip()
  zip.file('[Content_Types].xml', '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
  zip.file('_rels/.rels', '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
  zip.file('word/document.xml', '<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Unrelated first paragraph.</w:t></w:r></w:p><w:p><w:r><w:t>The cited limit is 1.5 millimeters.</w:t></w:r></w:p></w:body></w:document>')
  return zip.generateAsync({ type: 'blob', mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' })
}

export function sheetFixture(): Blob {
  const book = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet([['Equipment', 'Limit'], ['Anchor', '1.5 mm'], ['Cable', '15 mm']]), 'Inspection')
  return new Blob([XLSX.write(book, { type: 'array', bookType: 'xlsx' })])
}

export async function epubFixture(): Promise<Blob> {
  const zip = new JSZip()
  zip.file('mimetype', 'application/epub+zip')
  zip.file('META-INF/container.xml', '<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>')
  zip.file('OEBPS/content.opf', '<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item id="ch1" href="one.xhtml" media-type="application/xhtml+xml"/><item id="ch2" href="two.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/><itemref idref="ch2"/></spine></package>')
  zip.file('OEBPS/one.xhtml', '<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Repeated cited passage.</p></body></html>')
  zip.file('OEBPS/two.xhtml', '<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Repeated cited passage.</p></body></html>')
  return zip.generateAsync({ type: 'blob', mimeType: 'application/epub+zip' })
}

export async function docxImageFixture(count = 1): Promise<Blob> {
  const zip = await JSZip.loadAsync(await (await docxFixture()).arrayBuffer())
  zip.file('word/_rels/document.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="image1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image.png"/></Relationships>')
  zip.file('word/media/image.png', 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', { base64: true })
  const drawing = '<w:p><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><wp:extent cx="914400" cy="914400"/><wp:docPr id="1" name="Evidence"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill><a:blip xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" r:embed="image1"/></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="914400" cy="914400"/></a:xfrm></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>'
  const xml = await zip.file('word/document.xml')!.async('string')
  zip.file('word/document.xml', xml.replace('</w:body>', drawing.repeat(count) + '<w:p><w:r><w:t>Verified paragraph after the image.</w:t></w:r></w:p></w:body>'))
  return zip.generateAsync({ type: 'blob' })
}
