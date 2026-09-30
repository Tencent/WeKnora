export const mcpHeaderVariables = ['user.id', 'user.email', 'principal.id', 'principal.type', 'external.user_id', 'im.user_id', 'im.platform', 'tenant.id']
const validName = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/
const controls = /[\x00-\x1f\x7f]/
const credentials = new Set(['authorization', 'cookie', 'set-cookie', 'x-api-key'])
const identityHeaders = new Set(['x-external-user-token', 'x-external-user-id', 'x-tenant-id', 'x-weknora-desktop-token', 'x-embed-session', 'x-embed-visitor', 'x-user-id', 'x-user-email', 'x-authenticated-user', 'x-authenticated-email'])
const protocolHeaders = new Set(['host', 'connection', 'keep-alive', 'te', 'trailer', 'transfer-encoding', 'upgrade', 'content-length', 'content-type', 'accept', 'accept-encoding', 'forwarded', 'mcp-session-id', 'mcp-protocol-version', 'last-event-id'])
function protocol(name: string) { return protocolHeaders.has(name) || /^(proxy-|sec-|x-forwarded-|x-weknora-|x-principal-|x-auth-|x-workspace-|x-actor-|x-user-|x-tenant-|x-embed-)/.test(name) }

// Validate only: resolution is exclusively on the authenticated backend request.
function parse(value: string): { dynamic: boolean; error?: string } {
  let dynamic = false
  for (let i = 0; i < value.length;) {
    if (value.startsWith('\\{{', i)) { i += 3; continue }
    if (!value.startsWith('{{', i)) { i++; continue }
    dynamic = true
    const end = value.indexOf('}}', i + 2)
    if (end < 0) return { dynamic, error: 'unclosedExpression' }
    for (const candidate of value.slice(i + 2, end).split('??').map(v => v.trim())) {
      if (mcpHeaderVariables.includes(candidate)) continue
      if (!candidate.startsWith('request.headers.')) return { dynamic, error: 'invalidVariable' }
      const name = candidate.slice('request.headers.'.length).toLowerCase()
      if (!validName.test(name)) return { dynamic, error: 'invalidVariable' }
      if (protocol(name) || credentials.has(name) || identityHeaders.has(name)) return { dynamic, error: 'protectedHeader' }
    }
    i = end + 2
  }
  return { dynamic }
}

// Return an i18n reason key without echoing potentially sensitive values.
export function validateMCPHeaders(headers: {key: string; value: string}[], apiKeyHeader = ''): string | null {
  const seen = new Set<string>()
  for (const header of headers) {
    if (!header.key.trim() && !header.value.trim()) continue
    const name = header.key.trim().toLowerCase()
    if (!validName.test(name)) return 'invalidName'
    if (seen.has(name)) return 'duplicateName'
    seen.add(name)
    if (controls.test(header.value)) return 'invalidValue'
    const parsed = parse(header.value)
    if (parsed.error) return parsed.error
    if (parsed.dynamic && (protocol(name) || credentials.has(name) || name === apiKeyHeader.toLowerCase())) return 'protectedHeader'
  }
  return null
}

export function hasDynamicMCPHeaders(headers?: Record<string, string>): boolean {
  return Object.values(headers || {}).some(value => parse(value).dynamic)
}
