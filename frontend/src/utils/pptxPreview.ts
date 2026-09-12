import JSZip from 'jszip'

const CONTENT_TYPES_NS = 'http://schemas.openxmlformats.org/package/2006/content-types'
const RELATIONSHIPS_NS = 'http://schemas.openxmlformats.org/package/2006/relationships'
const PRESENTATION_NS = 'http://schemas.openxmlformats.org/presentationml/2006/main'
const SLIDE_MASTER_TYPE = 'application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml'

function parseXml(source: string): Document {
  const document = new DOMParser().parseFromString(source, 'application/xml')
  if (document.getElementsByTagName('parsererror').length) throw new Error('Invalid PPTX metadata')
  return document
}

/** Resolve an OPC relationship relative to its owning part, not its _rels directory. */
function relationshipTarget(relsPath: string, target: string): string {
  const base = relsPath === '_rels/.rels' ? '' : relsPath.slice(0, relsPath.lastIndexOf('/_rels/') + 1)
  return decodeURIComponent(new URL(target, `https://pptx.invalid/${base}`).pathname.slice(1))
}

/**
 * PptxGenJS 4.0.1 declares a master per slide even when only one master is stored.
 * Office ignores these unused entries; vue-office aborts parsing and returns zero
 * slides. Repair only missing, unreferenced master declarations in a preview copy.
 * Never remove a referenced part or alter the downloaded original.
 */
export async function preparePptxPreview(data: ArrayBuffer): Promise<{ data: ArrayBuffer; slideCount: number }> {
  const zip = await JSZip.loadAsync(data)
  const typesPart = zip.file('[Content_Types].xml')
  const presentationPart = zip.file('ppt/presentation.xml')
  if (!typesPart || !presentationPart) throw new Error('Invalid PPTX package')
  const types = parseXml(await typesPart.async('string'))
  const presentation = parseXml(await presentationPart.async('string'))
  const slideCount = presentation.getElementsByTagNameNS(PRESENTATION_NS, 'sldId').length
  if (!slideCount) throw new Error('PPTX contains no slides')

  const missingMasters = Array.from(types.getElementsByTagNameNS(CONTENT_TYPES_NS, 'Override')).filter(entry => {
    const partName = entry.getAttribute('PartName') || ''
    return entry.getAttribute('ContentType') === SLIDE_MASTER_TYPE
      && partName.startsWith('/ppt/slideMasters/') && !zip.file(decodeURIComponent(partName.slice(1)))
  })
  if (!missingMasters.length) return { data, slideCount }

  const references = new Set<string>()
  for (const part of Object.values(zip.files)) {
    if (part.dir || !part.name.endsWith('.rels')) continue
    const relations = parseXml(await part.async('string'))
    for (const rel of Array.from(relations.getElementsByTagNameNS(RELATIONSHIPS_NS, 'Relationship'))) {
      const target = rel.getAttribute('Target')
      if (target && rel.getAttribute('TargetMode') !== 'External') references.add(relationshipTarget(part.name, target))
    }
  }
  for (const entry of missingMasters) {
    const partName = decodeURIComponent(entry.getAttribute('PartName')!.slice(1))
    if (references.has(partName)) throw new Error('PPTX references a missing slide master')
    entry.parentNode!.removeChild(entry)
  }
  zip.file('[Content_Types].xml', new XMLSerializer().serializeToString(types))
  return { data: await zip.generateAsync({ type: 'arraybuffer' }), slideCount }
}

/** The renderer can swallow parsing errors and report successful empty/partial output. */
export function isCompletePptxRender(result: unknown, expectedSlides: number): boolean {
  const slides = (result as { slides?: unknown[] } | null)?.slides
  return expectedSlides > 0 && Array.isArray(slides) && slides.length === expectedSlides
}
