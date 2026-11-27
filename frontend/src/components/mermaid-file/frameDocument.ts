// Only packaged application scripts enter srcdoc. Diagram text arrives later via MessagePort.
export function frameDocument(renderer: string, bootstrap: string): string {
  const nonce = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('')
  const policy = `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'`
  const script = (code: string) =>
    `<script nonce="${nonce}">${code.replace(/<\/script/gi, '<\\/script')}</script>`
  return `<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${policy}"><meta name="referrer" content="no-referrer"><meta http-equiv="x-dns-prefetch-control" content="off"><body inert>${script(renderer)}${script(bootstrap)}`
}
