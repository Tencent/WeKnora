import { del, get, patch, post } from '@/utils/request'
import type { CredentialVaultItem, SecureFormSubmission } from '@/utils/secureForm'

export interface CredentialVaultList {
  data: CredentialVaultItem[]
  available: boolean
}

export interface CredentialVaultInput {
  title?: string
  site: string
  domains: string[]
  username: string
  password: string
}

export interface CredentialVaultChanges {
  title?: string
  domains?: string[]
  username?: string
  password?: string
}

export async function listCredentialVault(site?: string): Promise<CredentialVaultList> {
  const query = site ? `?site=${encodeURIComponent(site)}` : ''
  const res: any = await get(`/api/v1/me/credential-vault${query}`)
  return { data: Array.isArray(res?.data) ? res.data : [], available: Boolean(res?.available) }
}

export async function createCredentialVaultItem(input: CredentialVaultInput): Promise<CredentialVaultItem> {
  const res: any = await post('/api/v1/me/credential-vault', input)
  return res?.data
}

export async function updateCredentialVaultItem(id: string, changes: CredentialVaultChanges): Promise<CredentialVaultItem> {
  const res: any = await patch(`/api/v1/me/credential-vault/${encodeURIComponent(id)}`, changes)
  return res?.data
}

export async function deleteCredentialVaultItem(id: string): Promise<void> {
  await del(`/api/v1/me/credential-vault/${encodeURIComponent(id)}`)
}

/** Posts a secure form. The response never echoes a value. */
export async function submitSecureForm(
  promptId: string,
  body: SecureFormSubmission | { action: 'decline' },
): Promise<{ resumed: boolean }> {
  const res: any = await post(`/api/v1/agent/secure-forms/${encodeURIComponent(promptId)}`, body)
  return { resumed: Boolean(res?.resumed) }
}
