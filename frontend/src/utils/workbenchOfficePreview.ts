import type { JSZipObject, JSZipStreamHelper } from 'jszip'
import { workbenchFileLimit } from './sandboxWorkbench'

export const WORKBENCH_OFFICE_LIMITS = {
  entries: 2048,
  entryBytes: 8 * 1024 * 1024,
  expandedBytes: 32 * 1024 * 1024,
  spreadsheetCells: 100000,
} as const

// JSZip 3.10 ships this public API but omits it from JSZipObject's typings.
type StreamingZipEntry = JSZipObject & {
  internalStream(type: 'uint8array'): JSZipStreamHelper<Uint8Array>
}

export class WorkbenchOfficePreviewError extends Error {
  constructor(public readonly code: 'officeExternal' | 'officeInvalid' | 'officeTooLarge') {
    super(code)
  }
}

function invalid(): never {
  throw new WorkbenchOfficePreviewError('officeInvalid')
}

function archivePath(name: string): string {
  let decoded: string
  try { decoded = decodeURIComponent(name) } catch { return invalid() }
  if (!decoded || decoded.startsWith('/') || /[\\:\x00-\x1f\x7f?#]/.test(decoded)) invalid()
  if (decoded.split('/').some(part => !part || part === '.' || part === '..')) invalid()
  return decoded
}

function relationshipTarget(part: string, target: string, names: Set<string>): void {
  let decoded: string
  try { decoded = decodeURIComponent(target).trim() } catch { return invalid() }
  if (!decoded || /^[a-z][a-z0-9+.-]*:/i.test(decoded) || decoded.startsWith('//') || /[\\\x00-\x1f\x7f]/.test(decoded)) {
    throw new WorkbenchOfficePreviewError('officeExternal')
  }
  const path = decoded.split('#')[0]
  if (path.includes('?')) invalid()
  const owner = part === '_rels/.rels' ? '' : part.replace(/(^|\/)_rels\//, '$1').slice(0, -5)
  const segments = path.startsWith('/') ? [] : owner.split('/').slice(0, -1)
  for (const segment of path.split('/')) {
    if (!segment || segment === '.') continue
    if (segment === '..') {
      if (!segments.length) invalid()
      segments.pop()
    } else {
      if (segment.includes(':')) invalid()
      segments.push(segment)
    }
  }
  if (!names.has(path ? segments.join('/') : owner)) invalid()
}

function validateXml(bytes: Uint8Array, name: string, names: Set<string>): void {
  // The existing viewers expect UTF-8 XML. Reject malformed XML/DTDs first.
  const xml = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  if (/<!DOCTYPE|<!ENTITY/i.test(xml)) invalid()
  const document = new DOMParser().parseFromString(xml, 'application/xml')
  if (document.doctype || document.getElementsByTagName('parsererror').length) invalid()
  if (!name.toLowerCase().endsWith('.rels')) return
  const root = document.documentElement
  if (root.localName !== 'Relationships') invalid()
  for (const relationship of Array.from(root.children)) {
    if (relationship.localName !== 'Relationship') invalid()
    const mode = relationship.getAttribute('TargetMode')?.trim().toLowerCase()
    if (mode && mode !== 'internal') throw new WorkbenchOfficePreviewError('officeExternal')
    relationshipTarget(name, relationship.getAttribute('Target') || '', names)
  }
}

function readBounded(entry: JSZipObject, budget: number, signal?: AbortSignal): Promise<Uint8Array<ArrayBuffer>> {
  return new Promise((resolve, reject) => {
    const chunks: Uint8Array[] = []
    let length = 0
    let stopped = false
    const stream = (entry as StreamingZipEntry).internalStream('uint8array')
    const fail = (error: unknown) => {
      stopped = true
      stream.pause()
      chunks.length = 0
      signal?.removeEventListener('abort', abort)
      reject(error)
    }
    const abort = () => fail(new DOMException('Preview cancelled', 'AbortError'))
    signal?.addEventListener('abort', abort, { once: true })
    if (signal?.aborted) { abort(); return }
    // Count actual output, not attacker-controlled ZIP size headers.
    stream.on('data', chunk => {
      if (stopped) return
      length += chunk.byteLength
      if (length > budget) { fail(new WorkbenchOfficePreviewError('officeTooLarge')); return }
      chunks.push(chunk)
    }).on('error', fail).on('end', () => {
      if (stopped) return
      signal?.removeEventListener('abort', abort)
      const result = new Uint8Array(length)
      let offset = 0
      for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.byteLength }
      resolve(result)
    }).resume()
  })
}

/** Validate and rebuild only bounded, package-local parts for the existing viewers. */
export async function prepareWorkbenchOfficePreview(
  blob: Blob, format: 'pptx' | 'xlsx', maxBytes?: number, signal?: AbortSignal,
): Promise<ArrayBuffer> {
  if (blob.size > workbenchFileLimit(maxBytes)) throw new WorkbenchOfficePreviewError('officeTooLarge')
  signal?.throwIfAborted()
  try {
    const { default: JSZip } = await import('jszip')
    const zip = await JSZip.loadAsync(await blob.arrayBuffer(), { createFolders: false })
    const entries = Object.values(zip.files)
    if (entries.length > WORKBENCH_OFFICE_LIMITS.entries) throw new WorkbenchOfficePreviewError('officeTooLarge')
    const files = entries.filter(entry => !entry.dir)
    const names = new Set<string>()
    for (const entry of files) {
      // JSZip normalizes traversal; check the original name before trusting it.
      const name = archivePath(entry.unsafeOriginalName || entry.name)
      if (name !== archivePath(entry.name) || names.has(name)) invalid()
      names.add(name)
    }
    if (!names.has('[Content_Types].xml') || !names.has('_rels/.rels') ||
      !names.has(format === 'pptx' ? 'ppt/presentation.xml' : 'xl/workbook.xml')) invalid()

    const clean = new JSZip()
    let total = 0
    for (const entry of files) {
      signal?.throwIfAborted()
      const bytes = await readBounded(entry, Math.min(
        WORKBENCH_OFFICE_LIMITS.entryBytes, WORKBENCH_OFFICE_LIMITS.expandedBytes - total,
      ), signal)
      total += bytes.byteLength
      if (/\.(xml|rels)$/i.test(entry.name)) validateXml(bytes, entry.name, names)
      clean.file(entry.name, bytes, { createFolders: false })
    }
    signal?.throwIfAborted()
    // Never pass the original archive to a second ZIP parser: discard directory
    // entries, duplicate headers, permissions and any hidden/normalized paths.
    return await clean.generateAsync({ type: 'arraybuffer', compression: 'STORE' })
  } catch (error) {
    if (signal?.aborted || error instanceof WorkbenchOfficePreviewError) throw error
    return invalid()
  }
}
