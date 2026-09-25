/** The CLI appends /api/v1 itself; retain any reverse-proxy path prefix. */
export function buildCLIConnectCommand(apiBaseUrl: string, origin: string): string {
  let host = 'https://your-server.com'
  try {
    const url = new URL(apiBaseUrl, origin && origin !== 'null' ? origin : undefined)
    if (url.protocol === 'https:' || url.protocol === 'http:') {
      const path = url.pathname.replace(/\/+$/, '').replace(/\/api\/v1$/, '')
      host = `${url.origin}${path}`
    }
  } catch {
    // Desktop bindings can still be loading when the page first renders.
  }
  // POSIX shell quoting prevents URL characters from becoming shell syntax.
  const quotedHost = `'${host.replace(/'/g, `'"'"'`)}'`
  return `weknora profile add weknora --host ${quotedHost} --use &&\nweknora auth login`
}
