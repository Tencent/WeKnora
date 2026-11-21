export type SecureFormFieldType = 'text' | 'email' | 'password' | 'one_time_code' | 'number'

export interface SecureFormExtraField {
  name: string
  label?: string
  type: SecureFormFieldType
  required?: boolean
}

export interface SecureFormRequest {
  title: string
  kind?: string
  site: string
  domains?: string[]
  parts?: string[]
  prefill?: { username?: string }
  extra?: SecureFormExtraField[]
  allow_save?: boolean
  observed_origin?: string
}

export type SecureFormChoice = 'use_once' | 'save' | 'saved'

export interface CredentialVaultItem {
  id: string
  title: string
  site: string
  domains: string[]
  username: string
  last_used_at?: string
  updated_at?: string
}

export interface SecureFormDraft {
  choice: SecureFormChoice
  vaultItemId?: string
  values: Record<string, string>
}

export interface SecureFormSubmission {
  action: 'submit'
  choice: SecureFormChoice
  vault_item_id?: string
  values: Record<string, string>
}

export type OriginWarning = 'unknown' | 'mismatch' | 'insecure' | null

const LOGIN_PARTS = ['username', 'password']

export const isLogin = (r: SecureFormRequest) => r.kind === 'login'

export function formParts(r: SecureFormRequest): string[] {
  if (!isLogin(r)) return []
  const asked = r.parts?.length ? r.parts : LOGIN_PARTS
  return LOGIN_PARTS.filter((p) => asked.includes(p))
}

export function allowsSave(r: SecureFormRequest): boolean {
  return isLogin(r) && formParts(r).includes('password') && r.allow_save !== false
}

export function requestDomains(r: SecureFormRequest): string[] {
  return [...new Set([r.site, ...(r.domains || [])].filter(Boolean))]
}

/** The hosts a fill may use: a saved login is limited to what it allows. */
export function fillDomains(r: SecureFormRequest, item?: CredentialVaultItem): string[] {
  const asked = requestDomains(r)
  return item ? asked.filter((h) => item.domains.includes(h)) : asked
}

export function originWarning(r: SecureFormRequest): OriginWarning {
  if (!r.observed_origin) return 'unknown'
  let u: URL
  try {
    u = new URL(r.observed_origin)
  } catch {
    return 'unknown'
  }
  if (u.hostname.toLowerCase() !== r.site.toLowerCase()) return 'mismatch'
  if (u.protocol !== 'https:') return 'insecure'
  return null
}

export function editableFields(r: SecureFormRequest, choice: SecureFormChoice): string[] {
  const extra = (r.extra || []).map((f) => f.name)
  return choice === 'saved' ? extra : [...formParts(r), ...extra]
}

export function requiredFields(r: SecureFormRequest, choice: SecureFormChoice): string[] {
  const extra = (r.extra || []).filter((f) => f.required).map((f) => f.name)
  return choice === 'saved' ? extra : [...formParts(r), ...extra]
}

export function buildSecureFormSubmission(r: SecureFormRequest, d: SecureFormDraft): SecureFormSubmission {
  const values: Record<string, string> = {}
  for (const name of editableFields(r, d.choice)) {
    const raw = d.values[name] ?? ''
    const value = name === 'username' ? raw.trim() : raw
    if (value !== '') values[name] = value
  }
  const out: SecureFormSubmission = { action: 'submit', choice: d.choice, values }
  if (d.choice === 'saved' && d.vaultItemId) out.vault_item_id = d.vaultItemId
  return out
}

export function canSubmitSecureForm(r: SecureFormRequest, s: SecureFormSubmission): boolean {
  if (s.choice === 'saved' && !s.vault_item_id) return false
  if (s.choice === 'save' && !allowsSave(r)) return false
  return requiredFields(r, s.choice).every((name) => (s.values[name] ?? '').trim() !== '')
}

export function inputTypeFor(t: SecureFormFieldType): 'text' | 'password' | 'number' {
  if (t === 'password') return 'password'
  if (t === 'number') return 'number'
  return 'text'
}

export function autocompleteFor(t: SecureFormFieldType): string {
  if (t === 'one_time_code') return 'one-time-code'
  if (t === 'password') return 'new-password'
  return 'off'
}
