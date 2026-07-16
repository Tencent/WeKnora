export interface CredentialFieldRenderOptions {
  secret?: boolean
  multiline?: boolean
}

export type CredentialFieldRenderKind = 'input' | 'password' | 'textarea' | 'secret-textarea'

export function getCredentialFieldRenderKind(field: CredentialFieldRenderOptions): CredentialFieldRenderKind {
  if (field.secret && field.multiline) return 'secret-textarea'
  if (field.secret) return 'password'
  if (field.multiline) return 'textarea'
  return 'input'
}
