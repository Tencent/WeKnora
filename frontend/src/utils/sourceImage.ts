/** Byte identity: never infer an embedded image's position from its filename or OCR. */
export async function sourceImageDigest(bytes: ArrayBuffer): Promise<string> {
  const hash = await crypto.subtle.digest('SHA-256', bytes)
  return [...new Uint8Array(hash)].map(b => b.toString(16).padStart(2, '0')).join('')
}

export async function indexEmbeddedSourceImages(root: Element): Promise<void> {
  await Promise.all([...root.querySelectorAll<HTMLImageElement>('img')].map(async image => {
    // Only bytes embedded by docx-preview; never fetch remote document links.
    const match = /^data:(?:image\/[^;,]+|application\/octet-stream);base64,([A-Za-z0-9+/=\s]+)$/.exec(image.src)
    if (!match) return
    try {
      const bytes = Uint8Array.from(atob(match[1]!), c => c.charCodeAt(0))
      image.dataset.sourceImageDigest = await sourceImageDigest(bytes.buffer)
    } catch { /* An unsupported image cannot establish identity. */ }
  }))
}
